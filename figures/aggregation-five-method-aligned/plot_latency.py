from plot_comparison import input_rows, plot_single

if __name__ == "__main__":
    plot_single(input_rows(), 'latency_s', 'Mean transaction latency (s)', 'latency')
