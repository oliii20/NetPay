# Figure text changes before submission

The numerical data and geometry of the figures can remain unchanged. Only the following embedded labels should be replaced in the source plotting files and re-exported:

1. `Fig1.pdf`: change the lower-right best-case label to **full matching** and the lower flow label to **Matched 100↔80**. The diagram can otherwise remain unchanged.
2. `Fig2.pdf`: rename **Solver** to **Matching coordinator** so that the figure uses the same role name as the paper.
3. `bidirectional_netting_potential_heatmap.pdf`: rename the color-bar title to **Matching ratio**.
4. `fig_exp1_scale_tps_bar.pdf`: rename the method label in the legend to **Matching**.
5. `fig_exp1_scale_latency_bar.pdf`: rename the method label in the legend to **Matching**.
6. `fig_repeat5_shard8_txpool_mean.pdf`: rename the method label in the legend to **Matching**.
7. `tx_confirmation_latency_violin.pdf`: rename the corresponding x-axis method label to **Matching**, and change its p95 annotation to **Matching p95**.
8. `ctx_stage_latency.pdf`: rename the corresponding x-axis method label to **Matching**.
9. `fig_shard8_ablation_window_repeat5.pdf`: change the panel title to **Matching-window ablation** and the x-axis title to **Maximum matching window (ms)**.

No wording change is needed in `matching_time.pdf`, `shard8_matching_rate_heatmap.pdf`, `fig_exp1_scale_matching_fallback_rate_boxplot.pdf`, or `fig_shard8_ablation_injection_repeat5.pdf`.
