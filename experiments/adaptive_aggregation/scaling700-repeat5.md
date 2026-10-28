# 30 万笔、700 tx/s/分片、4/8/16/24 分片重复实验

## 百万笔、不跑 CLPA（80 次）

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_repeated_scaling.py \
  --tx-number 1000000 \
  --methods adaptive,full,monoxide,brokerchain \
  --output "exp/scaling-700-1m-no-clpa-repeat5-$(date +%Y%m%d-%H%M%S)"
```

覆盖 4/8/16/24 分片，四个方案各重复 5 次，共 80 次，完成后自动绘制四方案对比图。百万笔自动使用原始完整数据集；区块上限 2000，其余参数与下述套件一致。`--methods` 同时传给运行与绘图脚本，不会启动或要求 CLPA 结果。

从仓库根目录运行：

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_repeated_scaling.py --tx-number 300000
```

脚本一次性编译当前代码，串行执行 4 个分片配置 × 5 个方案 × 5 次，共 100 次。每轮轮换方案先后顺序，避免某个方案总是最先执行。每个普通分片 4 副本，聚合方案增加 4 个 Beacon 副本。各次使用相同数据、同一批二进制，保存 SHA256。不会复用历史结果。

| 图中方案 | 脚本模式 | 路径 |
|---|---|---|
| Proposed (adaptive) | adaptive | 从全聚合开始，冻结排队超过 500 ms 时降载 |
| Proposed (full) | full | 始终全聚合，保留与 adaptive 相同的源端交易和提交后路由路径 |
| Monoxide | monoxide | static_relay |
| BrokerChain | brokerchain | static_broker；50 个纯随机 broker，seed=1，整套共用同一名单 |
| CLPA | clpa | clpa_relay，20 秒 CLPA 周期 |

每次全局 300000 笔正金额非合约有效交易；原始 CSV 与此前 30 万笔相同。700 为每分片配置注入速率，总配置速率为分片数×700，实测 TPS 仍按最终完成原交易统计。普通区块上限 2000、区块间隔 2000 ms、Beacon 间隔 1000 ms、聚合窗口 2000 ms。无人工 Beacon 延迟。自适应低水位 100 ms、冷却 10000 ms、恢复 3 个 2000 ms 窗口、相对首次降载后基准涨跌严格超过 75% 才考虑恢复，最低保留 25% 目标。

结果默认位于 exp/scaling-700-300000-repeat5-时间戳/。每次原有 CSV、逐笔生命周期、区块与交易池记录、回执统计、策略及冻结队列时间序列、日志、数据库、配置和完成性检查均保留。每次最大等待 3600 秒。所有实验通过后自动画图，输出套件目录 figures/。

输出图：

1. TPS 随分片数变化，均值与样本标准差。
2. 平均、P95、P99 确认时延，以及 TPS/平均时延组合图。
3. 目标分片减少的交易条目数量。
4. 减少的接收端账户更新数量；不将源端扣款计为节省。
5. 8 分片交易池曲线：8 个 leader 之和，5 次均值与标准差阴影；仅显示每个方案全部轮次覆盖的共同时间段，不把提前结束的运行补零。
6. 8 分片确认时延小提琴图：合并全部 5 次逐笔数据，KDE 使用固定种子抽样；叠加精确均值、中位数、IQR 与 P5–P95。
7. 8 分片全体原交易及仅跨片交易的分阶段时延，按轮次均值汇总；总时延误差条为轮次间样本标准差。
8. Beacon 构建与本地验证耗时、共识耗时、估算通信量和序列化存储量；耗时是墙钟时间，非 CPU 时间，存储不包含数据库开销。
9. 冻结平均/最大等待、积压数量峰值与普通 Relay2 数量。
10. 每种分片配置每轮的冻结积压、等待和聚合目标数时间序列，标注降载、恢复、注入结束。

所有图提供 PDF、PNG（300 dpi）、SVG，并汇入 all_figures.pdf；index.html 可浏览整套图。CSV 保留每轮数值，performance_summary.csv 保存性能均值/样本标准差；source_audit.json 记录数据与代码一致性。5 次标准差不是置信区间；无切换时不能将 proposed 两组的运行波动解释为动态机制收益。

只检查配置而不启动任何节点，可运行（使用新目录）：

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_repeated_scaling.py \
  --tx-number 300000 --dry-run --output "exp/scaling700-plan-$(date +%Y%m%d-%H%M%S)"
```

如果实验已全部完成，只需绘图：

```bash
/opt/anaconda3/bin/python3 figures/gen_fig_repeated_scaling.py \
  "exp/实际的套件目录" --repeats 5 --output "exp/实际的套件目录/figures-new"
```

绘图输出目录必须不存在，避免覆盖已有图。新绘图脚本会检查 100 个配置完整、无重复/缺失轮次、完成数量、零告警/错误、同数据/二进制与配置哈希，再读取全部重复实验。不会只取第一轮的 8 分片数据。
