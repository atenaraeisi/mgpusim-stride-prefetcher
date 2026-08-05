# Stride Prefetcher for the L2 Cache in MGPUSim

This repository contains the implementation, integration, testing, and evaluation of a configurable **stride prefetcher** for the L2 cache in [MGPUSim](https://github.com/sarchlab/mgpusim), built on top of the [Akita](https://github.com/sarchlab/akita) simulation framework.

The prefetcher observes demand read requests, detects regular memory-access strides, and generates future memory requests before they are explicitly requested by the GPU.

---

## Team Members

1. **Atena Raeisi** — Student ID: `403105952`
2. **Shadi Ghabel** — Student ID: `403171113`
3. **Parnia Dabbagh** — Student ID: `403110647`

---

## Project Overview

The goal of this project is to study the effect of hardware prefetching on GPU memory-system performance.

A configurable stride prefetcher was added to the MGPUSim L2 cache. The implementation supports both baseline execution without prefetching and stride-prefetching execution with configurable parameters.

The project includes:

- A common prefetcher interface
- A configurable stride-detection algorithm
- Integration with the L2 write-back cache
- Command-line configuration
- Separate demand and prefetch statistics
- Unit and integration tests
- Benchmark execution using FIR and matrix multiplication
- Python scripts for metric analysis and plot generation

---

## Main Features

The stride prefetcher supports:

- Positive and negative strides
- Cache-line address alignment
- Separate prediction state for each process ID
- Configurable prefetch degree
- Configurable confidence threshold
- Configurable history size
- Optional page-boundary protection
- Detection of changes in the access pattern
- Ignoring zero-stride accesses
- Address overflow and underflow protection
- Resetting the internal predictor state
- Integration with cache, MSHR, and memory-request processing
- Separate statistics for demand and prefetch requests

---

## How the Stride Prefetcher Works

For every demand read request, the prefetcher performs the following steps:

1. The requested address is aligned to the beginning of its cache line.
2. The address is added to the access history associated with its process ID.
3. The difference between the current and previous cache-line addresses is calculated.
4. If the same stride is observed repeatedly, the confidence value is increased.
5. When the confidence threshold is reached, future addresses are generated using the detected stride.
6. The number of generated addresses is controlled by the prefetch degree.
7. If page-boundary protection is enabled, predictions stop before crossing into another page.
8. If the access pattern changes, prefetching stops until the new stride becomes stable.

The implementation uses a signed stride, which allows it to detect both increasing and decreasing address sequences.

---

## Repository Structure

```text
mgpusim-stride-prefetcher/
├── akita/
│   └── mem/cache/
│       ├── prefetcher.go
│       ├── stride.go
│       ├── stride_test.go
│       └── writeback/
│           ├── builder.go
│           ├── comp.go
│           ├── topparser.go
│           ├── directorystage.go
│           ├── bankstage.go
│           ├── prefetch_integration_test.go
│           └── ...
│
├── mgpusim/
│   └── amd/samples/
│       ├── fir/
│       ├── matrixmultiplication/
│       └── runner/
│           ├── flag.go
│           ├── runner.go
│           ├── report.go
│           └── timingconfig/
│
├── scripts/
│   ├── analyze_metrics.py
│   └── plot_results.py
│
├── results/
│   ├── final/
│   │   ├── fir_65536/
│   │   ├── matmul_128/
│   │   └── matmul_512/
│   └── plots/
│
├── go.work
├── go.work.sum
└── README.md
```

---

## Key Implementation Files

### Prefetcher Interface

```text
akita/mem/cache/prefetcher.go
```

Defines the common interface used by cache prefetchers:

- `Inspect`
- `GetPrefetchAddresses`
- `Reset`

### Stride Algorithm

```text
akita/mem/cache/stride.go
```

Contains:

- `StridePrefetcher`
- `StridePrefetcherConfig`
- Per-process prediction state
- Stride calculation
- Confidence tracking
- Address generation
- Page-boundary checking
- Overflow and underflow protection

### Stride Unit Tests

```text
akita/mem/cache/stride_test.go
```

Tests:

- Positive strides
- Negative strides
- Prefetch degree
- Confidence threshold
- Pattern changes
- Zero strides
- Address alignment
- Independent process states
- Page-boundary behavior
- Invalid requests
- Reset behavior
- Address overflow and underflow

### Cache Integration

```text
akita/mem/cache/writeback/
```

Contains the changes required to connect the prefetcher to the write-back L2 cache and distinguish demand transactions from prefetch transactions.

### Runner Configuration

```text
mgpusim/amd/samples/runner/flag.go
```

Defines the command-line parameters used to enable and configure the prefetcher.

### Metrics Collection

```text
mgpusim/amd/samples/runner/report.go
```

Collects execution-time, cache, MSHR, and prefetch-related metrics.

---

## Requirements

The project uses a Go workspace containing the Akita and MGPUSim modules.

Required software:

- Linux
- Go, using the version specified in `go.work`
- Python 3
- `pandas`
- `matplotlib`
- SQLite command-line tools, when exporting metrics from SQLite

Install the Python dependencies with:

```bash
python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install pandas matplotlib
```

---

## Clone and Initialize

```bash
git clone https://github.com/atenaraeisi/mgpusim-stride-prefetcher.git
cd mgpusim-stride-prefetcher
go work sync
```

---

## Running the Tests

### Stride-prefetcher unit tests

From the repository root:

```bash
cd akita
go test -count=1 -v ./mem/cache
```

### Write-back cache and integration tests

```bash
go test -count=1 -v ./mem/cache/writeback
```

### All Akita tests

```bash
go test -count=1 ./...
```

A successful execution of these tests verifies the stride-prediction logic and its integration with the write-back cache.

---

## Prefetcher Parameters

The following command-line flags are available:

| Parameter | Default | Description |
|---|---:|---|
| `-prefetcher` | `none` | Selects `none` or `stride` |
| `-prefetch-degree` | `2` | Number of future cache-line addresses generated per prediction |
| `-prefetch-confidence` | `2` | Number of matching strides required before prefetching |
| `-prefetch-history-size` | `8` | Maximum number of addresses stored in each process history |
| `-prefetch-prevent-page-crossing` | `true` | Prevents generated requests from crossing page boundaries |
| `-metric-file-name` | `metrics` | Sets the name of the generated metric output |

The internal stride-prefetcher defaults use:

- Cache-line size: `64 bytes`
- Page size: `4096 bytes`

---

## Running the FIR Benchmark

Move to the FIR sample directory:

```bash
cd mgpusim/amd/samples/fir
go build -o fir .
```

### Baseline execution

```bash
./fir \
  -timing \
  -verify \
  -disable-rtm \
  -length=65536 \
  -taps=16 \
  -prefetcher=none \
  -report-all \
  -metric-file-name=baseline
```

### Stride-prefetcher execution

```bash
./fir \
  -timing \
  -verify \
  -disable-rtm \
  -length=65536 \
  -taps=16 \
  -prefetcher=stride \
  -prefetch-degree=2 \
  -prefetch-confidence=2 \
  -prefetch-history-size=8 \
  -prefetch-prevent-page-crossing=true \
  -report-all \
  -metric-file-name=stride
```

The degree, confidence threshold, history size, and page-crossing option can be changed to evaluate different configurations.

---

## Running the Matrix-Multiplication Benchmark

Move to the matrix-multiplication sample directory:

```bash
cd mgpusim/amd/samples/matrixmultiplication
go build -o matrixmultiplication .
```

### Baseline execution

```bash
./matrixmultiplication \
  -timing \
  -verify \
  -disable-rtm \
  -x=128 \
  -y=128 \
  -z=128 \
  -prefetcher=none \
  -report-all \
  -metric-file-name=baseline
```

### Stride-prefetcher execution

```bash
./matrixmultiplication \
  -timing \
  -verify \
  -disable-rtm \
  -x=128 \
  -y=128 \
  -z=128 \
  -prefetcher=stride \
  -prefetch-degree=2 \
  -prefetch-confidence=2 \
  -prefetch-history-size=8 \
  -prefetch-prevent-page-crossing=true \
  -report-all \
  -metric-file-name=stride
```

The matrix dimensions can be changed using the `-x`, `-y`, and `-z` parameters.

---

## Exporting Metrics

The simulator stores its measurements in the `mgpusim_metrics` table.

To export this table from a generated SQLite database:

```bash
sqlite3 <database-file>.sqlite3 \
  -header \
  -csv \
  "SELECT * FROM mgpusim_metrics;" \
  > mgpusim_metrics.csv
```

Replace `<database-file>.sqlite3` with the actual name of the generated database.

---

## Analyzing a Single Execution

From the repository root:

```bash
python3 scripts/analyze_metrics.py path/to/mgpusim_metrics.csv
```

The script aggregates metrics across all L2 cache banks and reports derived measurements.

---

## Comparing Baseline and Stride Executions

```bash
python3 scripts/analyze_metrics.py \
  --compare \
  path/to/baseline.csv \
  path/to/stride.csv
```

This command prints a baseline-versus-stride comparison and creates:

```text
comparison_summary.csv
```

---

## Evaluation Metrics

The analysis scripts calculate and report:

- Kernel execution time
- Total demand reads
- Demand cache hits
- Demand cache misses
- Demand cache hit rate
- Demand cache miss rate
- Demand MSHR hits
- Demand MSHR hit rate
- Demand MSHR miss rate
- Number of prefetch requests
- Prefetch cache hits
- Prefetch MSHR hits
- Prefetch misses
- Useful prefetches
- Prefetch usage rate
- Prefetch coverage
- Cache-pollution count
- Cache-pollution rate
- Speedup over the baseline

Demand and prefetch transactions are recorded separately to prevent prefetch traffic from incorrectly affecting demand cache statistics.

---

## Generating Plots

The plotting script automatically discovers benchmark directories under `results/final`.

Run:

```bash
python3 scripts/plot_results.py
```

The equivalent explicit command is:

```bash
python3 scripts/plot_results.py \
  --results-dir results/final \
  --output-dir results/plots \
  --formats png pdf
```

The script generates report-ready plots for:

1. Execution time
2. L2 demand cache hit rate
3. L2 demand cache miss rate
4. Demand MSHR hit rate
5. Demand MSHR miss rate
6. Prefetch coverage
7. Prefetch usage rate
8. Cache-pollution count
9. Cache-pollution rate
10. Speedup over the baseline

It also creates a combined summary file:

```text
results/plots/all_metrics_summary.csv
```

---

## Experimental Results

Final benchmark data is organized under:

```text
results/final/
├── fir_65536/
├── matmul_128/
└── matmul_512/
```

Each benchmark directory contains baseline results, stride-prefetcher results, and generated comparison summaries.

Generated plots are stored under:

```text
results/plots/
```

The experiments compare baseline execution against different stride-prefetcher configurations and evaluate the trade-off between useful prefetching, additional memory traffic, cache behavior, and execution time.

---

## Upstream Projects

This project extends the following open-source simulation frameworks:

- [Akita](https://github.com/sarchlab/akita)
- [MGPUSim](https://github.com/sarchlab/mgpusim)

The original source trees are included in this repository because modifications were required in both the cache implementation and the MGPUSim runner configuration.

---

## Academic Use

This repository was developed as an academic computer-architecture project for implementing and evaluating memory-prefetching mechanisms in a GPU simulator.
