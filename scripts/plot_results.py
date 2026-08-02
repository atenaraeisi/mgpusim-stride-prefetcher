#!/usr/bin/env python3
"""Generate report-ready plots from results/final comparison CSV files.

The script discovers benchmark folders automatically. Each folder is expected to
contain:
  - baseline.csv
  - one or more stride*.csv files
  - one or more comparison_summary*.csv files produced by analyze_metrics.py

Default usage from the repository root:
    python3 scripts/plot_results.py

Optional arguments:
    python3 scripts/plot_results.py \
        --results-dir results/final \
        --output-dir results/plots \
        --formats png pdf
"""

from __future__ import annotations

import argparse
import math
import re
import sys
from pathlib import Path
from typing import Iterable

import matplotlib

matplotlib.use("Agg")

import matplotlib.pyplot as plt
import pandas as pd


SUMMARY_PREFIX = "comparison_summary"

PLOT_SPECS = [
    {
        "filename": "01_execution_time",
        "metric": "kernel_time_s",
        "title": "Execution Time",
        "ylabel": "Time (microseconds)",
        "kind": "runtime",
        "include_baseline": True,
    },
    {
        "filename": "02_cache_hit_rate",
        "metric": "demand_cache_hit_rate",
        "title": "L2 Demand Cache Hit Rate",
        "ylabel": "Hit rate (%)",
        "kind": "percent",
        "include_baseline": True,
    },
    {
        "filename": "03_cache_miss_rate",
        "metric": "demand_cache_miss_rate",
        "title": "L2 Demand Cache Miss Rate",
        "ylabel": "Miss rate (%)",
        "kind": "percent",
        "include_baseline": True,
    },
    {
        "filename": "04_mshr_hit_rate",
        "metric": "demand_mshr_hit_rate",
        "title": "Demand MSHR Hit Rate",
        "ylabel": "MSHR hit rate (%)",
        "kind": "percent",
        "include_baseline": True,
    },
    {
        "filename": "05_mshr_miss_rate",
        "metric": "demand_mshr_miss_rate",
        "title": "Demand MSHR Miss Rate",
        "ylabel": "MSHR miss rate (%)",
        "kind": "percent",
        "include_baseline": True,
    },
    {
        "filename": "06_coverage",
        "metric": "coverage",
        "title": "Prefetch Coverage",
        "ylabel": "Coverage (%)",
        "kind": "percent",
        "include_baseline": False,
    },
    {
        "filename": "07_prefetch_usage_rate",
        "metric": "prefetch_usage_rate",
        "title": "Prefetch Usage Rate",
        "ylabel": "Useful prefetches (%)",
        "kind": "percent",
        "include_baseline": False,
    },
    {
        "filename": "08_cache_pollution_count",
        "metric": "cache_pollution",
        "title": "Unused Prefetched Blocks Evicted",
        "ylabel": "Evicted unused prefetched blocks",
        "kind": "count",
        "include_baseline": True,
    },
    {
        "filename": "09_pollution_rate",
        "metric": "pollution_rate",
        "title": "Measured Cache Pollution Rate",
        "ylabel": "Pollution events / prefetch requests (%)",
        "kind": "percent",
        "include_baseline": False,
    },
    {
        "filename": "10_speedup",
        "metric": "speedup",
        "title": "Speedup over Baseline",
        "ylabel": "Speedup (baseline time / run time)",
        "kind": "speedup",
        "include_baseline": False,
    },
]


def benchmark_label(folder_name: str) -> str:
    """Convert folder names such as matmul_128 to report-friendly labels."""
    parts = folder_name.split("_")
    pretty = []
    for part in parts:
        lower = part.lower()
        if lower == "fir":
            pretty.append("FIR")
        elif lower == "matmul":
            pretty.append("MatMul")
        else:
            pretty.append(part)
    return " ".join(pretty)


def configuration_label(stride_csv: Path) -> str:
    """Build a concise legend label from a stride CSV filename."""
    stem = stride_csv.stem
    if stem == "stride":
        return "Stride"

    tokens = stem.split("_")[1:]
    labels = []
    no_page_check = False

    for token in tokens:
        lower = token.lower()
        if lower == "nopage":
            no_page_check = True
        elif re.fullmatch(r"d\d+", lower):
            labels.append(lower.upper())
        elif re.fullmatch(r"c\d+", lower):
            labels.append(lower.upper())
        else:
            labels.append(token.replace("-", " "))

    label = "Stride"
    if labels:
        label += " " + " ".join(labels)
    if no_page_check:
        label += " (No Page Check)"
    return label


def summary_suffix(summary_csv: Path) -> str:
    """Return d1, d2, ... from comparison_summary_d1.csv, or an empty string."""
    suffix = summary_csv.stem[len(SUMMARY_PREFIX) :]
    return suffix.lstrip("_")


def find_stride_csv(benchmark_dir: Path, summary_csv: Path) -> Path | None:
    """Match a comparison summary with its raw stride CSV."""
    suffix = summary_suffix(summary_csv)

    if not suffix:
        exact = benchmark_dir / "stride.csv"
        if exact.exists():
            return exact
    else:
        candidates = sorted(
            benchmark_dir.glob(f"stride_{suffix}*.csv"),
            key=lambda path: (len(path.name), path.name),
        )
        if candidates:
            return candidates[0]

    all_stride = sorted(benchmark_dir.glob("stride*.csv"))
    if len(all_stride) == 1:
        return all_stride[0]

    return None


def load_summary(summary_csv: Path) -> pd.DataFrame:
    """Load and validate one comparison_summary CSV."""
    df = pd.read_csv(summary_csv)
    required = {"metric", "baseline", "stride"}
    missing = required - set(df.columns)
    if missing:
        raise ValueError(
            f"{summary_csv}: missing required columns: {', '.join(sorted(missing))}"
        )

    df = df.copy()
    df["metric"] = df["metric"].astype(str)
    df["baseline"] = pd.to_numeric(df["baseline"], errors="coerce")
    df["stride"] = pd.to_numeric(df["stride"], errors="coerce")
    return df.set_index("metric")


def load_kernel_time(csv_path: Path | None) -> float:
    """Read Driver/kernel_time from a raw MGPUSim metrics CSV."""
    if csv_path is None or not csv_path.exists():
        return float("nan")

    df = pd.read_csv(csv_path, usecols=["Location", "What", "Value"])
    rows = df[(df["Location"] == "Driver") & (df["What"] == "kernel_time")]
    if rows.empty:
        return float("nan")
    return float(rows.iloc[0]["Value"])


def metric_dict(summary: pd.DataFrame, column: str) -> dict[str, float]:
    return {metric: float(value) for metric, value in summary[column].items()}


def collect_results(results_dir: Path) -> pd.DataFrame:
    """Discover all benchmark/configuration results and return one tidy table."""
    if not results_dir.exists():
        raise FileNotFoundError(f"Results directory does not exist: {results_dir}")

    records: list[dict[str, object]] = []

    benchmark_dirs = sorted(path for path in results_dir.iterdir() if path.is_dir())
    for benchmark_dir in benchmark_dirs:
        summaries = sorted(benchmark_dir.glob(f"{SUMMARY_PREFIX}*.csv"))
        if not summaries:
            print(
                f"Warning: no {SUMMARY_PREFIX}*.csv files in {benchmark_dir}",
                file=sys.stderr,
            )
            continue

        benchmark = benchmark_label(benchmark_dir.name)
        baseline_csv = benchmark_dir / "baseline.csv"
        baseline_added = False

        for summary_csv in summaries:
            summary = load_summary(summary_csv)

            if not baseline_added:
                baseline_metrics = metric_dict(summary, "baseline")
                baseline_metrics.update(
                    {
                        "benchmark": benchmark,
                        "benchmark_dir": benchmark_dir.name,
                        "configuration": "Baseline",
                        "is_baseline": True,
                        "raw_csv": str(baseline_csv),
                        "summary_csv": str(summary_csv),
                        "kernel_time_s": load_kernel_time(baseline_csv),
                    }
                )
                records.append(baseline_metrics)
                baseline_added = True

            stride_csv = find_stride_csv(benchmark_dir, summary_csv)
            if stride_csv is None:
                print(
                    f"Warning: could not match a stride CSV to {summary_csv}",
                    file=sys.stderr,
                )

            stride_metrics = metric_dict(summary, "stride")
            stride_metrics.update(
                {
                    "benchmark": benchmark,
                    "benchmark_dir": benchmark_dir.name,
                    "configuration": (
                        configuration_label(stride_csv)
                        if stride_csv is not None
                        else f"Stride {summary_suffix(summary_csv)}".strip()
                    ),
                    "is_baseline": False,
                    "raw_csv": str(stride_csv) if stride_csv is not None else "",
                    "summary_csv": str(summary_csv),
                    "kernel_time_s": load_kernel_time(stride_csv),
                }
            )
            records.append(stride_metrics)

    if not records:
        raise ValueError(f"No usable comparison summaries found under {results_dir}")

    result = pd.DataFrame(records)

    baseline_times = (
        result[result["is_baseline"]]
        .set_index("benchmark")["kernel_time_s"]
        .to_dict()
    )

    def calculate_speedup(row: pd.Series) -> float:
        if bool(row["is_baseline"]):
            return 1.0
        baseline_time = float(baseline_times.get(row["benchmark"], float("nan")))
        run_time = float(row["kernel_time_s"])
        if not math.isfinite(baseline_time) or not math.isfinite(run_time) or run_time == 0:
            return float("nan")
        return baseline_time / run_time

    result["speedup"] = result.apply(calculate_speedup, axis=1)
    return result


def config_sort_key(label: str) -> tuple[int, int, str]:
    if label == "Baseline":
        return (0, 0, label)
    if label == "Stride":
        return (1, 0, label)

    degree_match = re.search(r"\bD(\d+)\b", label)
    degree = int(degree_match.group(1)) if degree_match else 10_000
    return (2, degree, label)


def format_values(values: Iterable[float], kind: str) -> list[str]:
    labels = []
    for value in values:
        if not math.isfinite(float(value)):
            labels.append("")
        elif kind == "percent":
            labels.append(f"{value:.3f}%")
        elif kind == "runtime":
            labels.append(f"{value:.2f}")
        elif kind == "count":
            labels.append(f"{value:,.0f}")
        elif kind == "speedup":
            labels.append(f"{value:.3f}x")
        else:
            labels.append(f"{value:.3f}")
    return labels


def prepare_metric_table(
    results: pd.DataFrame,
    metric: str,
    kind: str,
    include_baseline: bool,
) -> pd.DataFrame:
    data = results.copy()
    if not include_baseline:
        data = data[~data["is_baseline"]]

    if metric not in data.columns:
        return pd.DataFrame()

    data = data[["benchmark", "configuration", metric]].copy()
    data[metric] = pd.to_numeric(data[metric], errors="coerce")

    if kind == "percent":
        data[metric] *= 100.0
    elif kind == "runtime":
        data[metric] *= 1_000_000.0

    data = data.dropna(subset=[metric])
    if data.empty:
        return pd.DataFrame()

    benchmark_order = list(dict.fromkeys(results["benchmark"].tolist()))
    config_order = sorted(data["configuration"].unique(), key=config_sort_key)

    table = data.pivot(index="benchmark", columns="configuration", values=metric)
    table = table.reindex(index=[b for b in benchmark_order if b in table.index])
    table = table.reindex(columns=config_order)
    return table


def plot_metric(
    results: pd.DataFrame,
    spec: dict[str, object],
    output_dir: Path,
    formats: list[str],
    dpi: int,
) -> list[Path]:
    metric = str(spec["metric"])
    kind = str(spec["kind"])
    table = prepare_metric_table(
        results,
        metric,
        kind,
        bool(spec["include_baseline"]),
    )
    if table.empty:
        print(f"Warning: no finite data for {metric}; plot skipped", file=sys.stderr)
        return []

    figure_width = max(9.0, 2.2 * len(table.index) + 1.3 * len(table.columns))
    fig, ax = plt.subplots(figsize=(figure_width, 6.2))

    group_width = 0.82
    num_configs = max(1, len(table.columns))
    bar_width = group_width / num_configs
    centers = list(range(len(table.index)))

    for config_index, configuration in enumerate(table.columns):
        offset = -group_width / 2 + bar_width / 2 + config_index * bar_width
        positions = []
        values = []
        for benchmark_index, value in enumerate(table[configuration].tolist()):
            if pd.notna(value):
                positions.append(centers[benchmark_index] + offset)
                values.append(float(value))

        if not values:
            continue

        bars = ax.bar(
            positions,
            values,
            width=bar_width * 0.92,
            label=configuration,
        )
        ax.bar_label(
            bars,
            labels=format_values(values, kind),
            padding=3,
            fontsize=8,
            rotation=90,
        )

    ax.set_title(str(spec["title"]), pad=14, fontweight="bold")
    ax.set_xlabel("Benchmark")
    ax.set_ylabel(str(spec["ylabel"]))
    ax.set_xticks(centers)
    ax.set_xticklabels(table.index.tolist())
    ax.tick_params(axis="x", rotation=0)
    ax.grid(axis="y", linestyle="--", alpha=0.35)
    ax.set_axisbelow(True)

    finite_values = table.to_numpy().ravel()
    finite_values = [float(value) for value in finite_values if pd.notna(value)]
    if finite_values:
        maximum = max(finite_values)
        if kind == "percent":
            upper = min(100.0, max(1.0, maximum * 1.25))
            if maximum >= 85.0:
                upper = 100.0
            ax.set_ylim(0, upper)
        elif maximum >= 0:
            margin = 1.22 if maximum > 0 else 1.0
            ax.set_ylim(0, maximum * margin if maximum > 0 else 1.0)

    ax.legend(
        title="Configuration",
        frameon=False,
        ncol=min(3, max(1, len(table.columns))),
        loc="upper center",
        bbox_to_anchor=(0.5, -0.14),
    )

    fig.tight_layout()
    fig.subplots_adjust(bottom=0.24)

    saved: list[Path] = []
    for extension in formats:
        output_path = output_dir / f"{spec['filename']}.{extension}"
        save_kwargs = {"bbox_inches": "tight"}
        if extension.lower() == "png":
            save_kwargs["dpi"] = dpi
        fig.savefig(output_path, **save_kwargs)
        saved.append(output_path)

    plt.close(fig)
    return saved


def parse_args() -> argparse.Namespace:
    repo_root = Path(__file__).resolve().parents[1]

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--results-dir",
        type=Path,
        default=repo_root / "results" / "final",
        help="directory containing benchmark result folders",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=repo_root / "results" / "plots",
        help="directory where plots and the combined summary are saved",
    )
    parser.add_argument(
        "--formats",
        nargs="+",
        choices=["png", "pdf", "svg"],
        default=["png", "pdf"],
        help="one or more output formats",
    )
    parser.add_argument(
        "--dpi",
        type=int,
        default=300,
        help="PNG resolution",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    results_dir = args.results_dir.resolve()
    output_dir = args.output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    results = collect_results(results_dir)

    summary_path = output_dir / "all_metrics_summary.csv"
    results.to_csv(summary_path, index=False)

    saved_files: list[Path] = []
    for spec in PLOT_SPECS:
        saved_files.extend(
            plot_metric(
                results=results,
                spec=spec,
                output_dir=output_dir,
                formats=args.formats,
                dpi=args.dpi,
            )
        )

    print(f"Loaded {len(results)} runs from {results_dir}")
    print(f"Saved combined data: {summary_path}")
    print(f"Generated {len(saved_files)} plot files in {output_dir}")
    for path in saved_files:
        print(f"  {path.name}")


if __name__ == "__main__":
    main()
