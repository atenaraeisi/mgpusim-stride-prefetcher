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
    """Derive demand-only and prefetch-only metrics."""

    def g(name: str) -> float:
        return float(totals.get(name, 0.0))

    required = {
        "demand_read_hits",
        "demand_read_misses",
        "demand_read_mshr_hits",
        "prefetch_requests",
        "prefetch_hits",
        "prefetch_cache_hits",
        "prefetch_mshr_hits",
        "prefetch_misses",
        "cache_pollution",
    }

    missing = sorted(required - set(totals.index))
    if missing:
        raise ValueError(
            "CSV does not contain the new split counters: "
            + ", ".join(missing)
            + ". Rebuild and rerun the benchmark."
        )

    demand_hits = g("demand_read_hits")
    demand_misses = g("demand_read_misses")
    demand_mshr_hits = g("demand_read_mshr_hits")

    prefetch_requests = g("prefetch_requests")
    prefetch_hits = g("prefetch_hits")
    prefetch_cache_hits = g("prefetch_cache_hits")
    prefetch_mshr_hits = g("prefetch_mshr_hits")
    prefetch_misses = g("prefetch_misses")
    cache_pollution = g("cache_pollution")

    demand_cache_lookups = demand_hits + demand_misses
    demand_mshr_lookups = demand_mshr_hits + demand_misses

    total_demand_reads = (
        demand_hits
        + demand_misses
        + demand_mshr_hits
    )

    prefetch_outcomes = (
        prefetch_cache_hits
        + prefetch_mshr_hits
        + prefetch_misses
    )

    potential_demand_misses = (
        prefetch_hits
        + demand_misses
    )

    return {
        "total_demand_reads": total_demand_reads,

        "demand_read_hits": demand_hits,
        "demand_read_misses": demand_misses,

        "demand_cache_hit_rate": (
            demand_hits / demand_cache_lookups
            if demand_cache_lookups
            else float("nan")
        ),

        "demand_cache_miss_rate": (
            demand_misses / demand_cache_lookups
            if demand_cache_lookups
            else float("nan")
        ),

        "demand_read_mshr_hits": demand_mshr_hits,

        "demand_mshr_hit_rate": (
            demand_mshr_hits / demand_mshr_lookups
            if demand_mshr_lookups
            else float("nan")
        ),

        "demand_mshr_miss_rate": (
            demand_misses / demand_mshr_lookups
            if demand_mshr_lookups
            else float("nan")
        ),

        "prefetch_requests": prefetch_requests,
        "prefetch_cache_hits": prefetch_cache_hits,
        "prefetch_mshr_hits": prefetch_mshr_hits,
        "prefetch_misses": prefetch_misses,

        "prefetch_outcomes": prefetch_outcomes,

        "prefetch_unaccounted": (
            prefetch_requests - prefetch_outcomes
        ),

        "prefetch_hits": prefetch_hits,

        "prefetch_usage_rate": (
            prefetch_hits / prefetch_requests
            if prefetch_requests
            else float("nan")
        ),

        "coverage": (
            prefetch_hits / potential_demand_misses
            if potential_demand_misses
            else 0.0
        ),

        "cache_pollution": cache_pollution,

        "pollution_rate": (
            cache_pollution / prefetch_requests
            if prefetch_requests
            else float("nan")
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