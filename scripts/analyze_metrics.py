#!/usr/bin/env python3
"""Summarize mgpusim_metrics.csv (exported from a run's sqlite3 output) into
derived cache/prefetch metrics, with optional baseline-vs-stride comparison.

Usage:
    python3 analyze_metrics.py baseline_results/mgpusim_metrics.csv
    python3 analyze_metrics.py --compare \
        baseline_results/mgpusim_metrics.csv stride_results/mgpusim_metrics.csv
"""

import argparse
import re
import sys

import pandas as pd

L2_PATTERN = re.compile(r"GPU\[\d+\]\.L2Cache\[\d+\]")


def load_l2_totals(csv_path: str) -> pd.Series:
    """Aggregate each metric across all L2 cache banks."""
    df = pd.read_csv(csv_path)

    missing_cols = {"Location", "What", "Value"} - set(df.columns)
    if missing_cols:
        raise ValueError(f"{csv_path}: missing required columns {missing_cols}")

    l2 = df[df["Location"].str.match(L2_PATTERN, na=False)]
    if l2.empty:
        raise ValueError(f"{csv_path}: no rows matched GPU[x].L2Cache[y]")

    return l2.groupby("What")["Value"].sum()


def load_kernel_time(csv_path: str) -> float:
    """Total simulated execution time, from the Driver's kernel_time entry."""
    df = pd.read_csv(csv_path)
    row = df[(df["Location"] == "Driver") & (df["What"] == "kernel_time")]
    return float(row["Value"].iloc[0]) if not row.empty else float("nan")


def compute_derived_metrics(totals: pd.Series) -> dict:
    """Derive hit/miss/coverage/pollution rates from the raw counters."""

    def g(name: str) -> float:
        return float(totals.get(name, 0.0))

    read_hit = g("read-hit")
    read_miss = g("read-miss")
    mshr_hits = g("mshr_hits")
    mshr_misses = g("mshr_misses")
    prefetch_requests = g("prefetch_requests")
    prefetch_hits = g("prefetch_hits")
    prefetch_miss = g("prefetch-miss")
    cache_pollution = g("cache_pollution")

    total_read = read_hit + read_miss
    total_mshr = mshr_hits + mshr_misses
    # Demand-only misses (excludes prefetch-issued misses), since Coverage
    # measures demand misses avoided by prefetching.
    demand_misses = read_miss - prefetch_miss

    return {
        "read_hit": read_hit,
        "read_miss": read_miss,
        "hit_rate": read_hit / total_read if total_read else float("nan"),
        "miss_rate": read_miss / total_read if total_read else float("nan"),
        "mshr_hits": mshr_hits,
        "mshr_misses": mshr_misses,
        "mshr_hit_rate": mshr_hits / total_mshr if total_mshr else float("nan"),
        "mshr_miss_rate": mshr_misses / total_mshr if total_mshr else float("nan"),
        "prefetch_requests": prefetch_requests,
        "prefetch_hits": prefetch_hits,
        "prefetch_usage_rate": (
            prefetch_hits / prefetch_requests if prefetch_requests else float("nan")
        ),
        "coverage": prefetch_hits / demand_misses if demand_misses else float("nan"),
        "cache_pollution": cache_pollution,
        "pollution_rate": (
            cache_pollution / prefetch_requests if prefetch_requests else float("nan")
        ),
    }


def print_report(csv_path: str) -> dict:
    totals = load_l2_totals(csv_path)
    kernel_time = load_kernel_time(csv_path)
    derived = compute_derived_metrics(totals)
    derived["kernel_time_s"] = kernel_time

    print(f"\n=== {csv_path} ===")
    print(f"kernel_time: {kernel_time:.6e} s")
    print("-" * 50)
    for key, value in derived.items():
        if key == "kernel_time_s":
            continue
        if isinstance(value, float) and value != value:  # NaN
            print(f"{key:22s}: N/A")
        elif "rate" in key or key == "coverage":
            print(f"{key:22s}: {value:.2%}")
        else:
            print(f"{key:22s}: {value:.0f}")

    return derived


def compare(baseline_csv: str, stride_csv: str) -> None:
    base = print_report(baseline_csv)
    stride = print_report(stride_csv)

    print("\n=== Baseline vs. Stride ===")
    speedup = base["kernel_time_s"] / stride["kernel_time_s"]
    print(f"Speedup (baseline / stride): {speedup:.4f}x")

    rows = [
        {"metric": key, "baseline": base.get(key, float("nan")), "stride": val}
        for key, val in stride.items()
        if key != "kernel_time_s"
    ]
    comparison_df = pd.DataFrame(rows)
    print(comparison_df.to_string(index=False))

    out_path = "comparison_summary.csv"
    comparison_df.to_csv(out_path, index=False)
    print(f"\nSaved: {out_path}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", nargs="?", help="path to a single mgpusim_metrics.csv")
    parser.add_argument(
        "--compare", nargs=2, metavar=("BASELINE_CSV", "STRIDE_CSV"),
        help="compare two runs side by side",
    )
    args = parser.parse_args()

    if args.compare:
        compare(*args.compare)
    elif args.csv:
        print_report(args.csv)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()