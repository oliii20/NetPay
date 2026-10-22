#!/usr/bin/env python3
from plot_common import bar

if __name__ == "__main__":
    bar('ctx_latency_mean_ms', 'Mean CTX latency (s)', 'latency', 0.001)
