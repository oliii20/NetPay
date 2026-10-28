# 新代码性能重测与可控 Beacon 拥塞实验

所有模式使用同一份当前代码、同一 CSV 过滤后的前 N 笔正金额非合约交易。生成的 tx_source 配置统一设置 exclude_contract_txs: true 和 positive_value_only: true，过滤的零/负金额记录不占 tx_number 名额，broker 名单也从同一过滤后前缀选取。每个分片固定 4 个副本；提出方案另有 4 个 Beacon 副本。每套实验仅编译一次二进制，串行运行，记录数据和二进制 SHA256。输出目录必须不存在；失败保留配置、日志与 failure.json，不覆盖旧结果，不清理其他进程。

依赖：Go 1.25.7、Python + PyYAML；本机 Python 为 /opt/anaconda3/bin/python3。请从仓库根目录执行。不要并行运行其他基准任务。

## 1. 重测 4、8、16、24 分片

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_suite.py scaling \
  --shard-counts 4,8,16,24 --tx-number 300000 --per-shard-rate 600 \
  --repeats 3 --output "exp/newcode-scaling-$(date +%Y%m%d-%H%M%S)"
```

共 60 次：4 个分片配置 × 5 个方案 × 3 次。方案对应：

| 模式 | 实现 |
|---|---|
| monoxide | static_relay，关闭聚合 |
| brokerchain | static_broker，关闭聚合；相同数据前缀选择 5 个热门 + 45 个随机 broker，seed=1 |
| clpa | clpa_relay，关闭聚合；CLPA 周期默认 20 秒 |
| full | 新交易结构与源端普通扣款，提交后所有正金额跨片交易走聚合；零金额交易走普通 Relay2 |
| adaptive | 同一新路径，从全聚合开始，冻结队列等待严格超过 500ms 时动态调整 |

full 的 adaptive_routing.enabled 仍为 true，只有 min_destination_percent=100；adaptive 为 25。关闭 adaptive_routing 会回到旧的源端路径，不能作为公平全聚合对照。

每次 30 万笔；600 是每分片注入速率，因此总速率为分片数 × 600。普通分片区块间隔 2s，区块上限 2000，Beacon 间隔 1s，聚合窗口 2s。此套实验模拟服务成本为 0。轮次交错方案顺序，保存 comparison_runs.csv 和 comparison_summary.csv（均值与样本标准差）。TPS 按去重原交易数 / 最早创建到最晚最终确认的跨度计算；时延合并聚合与普通 Relay 的逐笔最终确认数据，不使用各分片 TPS 的平均。

只重测提出方案可加 --methods full,adaptive。可先加 --dry-run 检查配置；试运行目录不能再用作实际输出目录。

只重跑 BrokerChain 并使用纯随机 broker，可设 --methods brokerchain --broker-hot 0 --broker-seed 1。总数仍为 50，从当前有效交易前缀涉及的不同账户中均匀随机无放回抽样，不强制加入热门账户；四个分片配置共用同一名单，seed 固定便于复现。默认 --broker-hot 5 保留原套件的 5 热门 + 45 随机设定。--bins 指向上次套件的 bin/ 时直接复用既有二进制，不重新编译或覆盖它们。

## 2. 24 分片可控拥塞成对实验

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_suite.py congestion \
  --shards 24 --tx-number 300000 --per-shard-rate 600 \
  --freeze-high-ms 500 --freeze-low-ms 100 --cooldown-ms 10000 \
  --beacon-work-us 0 --repeats 3 \
  --output "exp/newcode-congestion-$(date +%Y%m%d-%H%M%S)"
```

共 6 次，full/adaptive 交替先后。两组使用相同二进制、工作负载和窗口，唯一策略差异为最低聚合目标比例 100% / 25%。默认模拟成本为零，验证真实交易负载下的自然排队；没有发生拥塞或切换也是有效观测，不自动增加人工成本。

提高 --per-shard-rate 可增加输入，但固定 30 万笔时也会缩短注入时间。--block-limit 和 --block-interval-ms 控制普通分片的源端出块能力；前者不是 Beacon 聚合批次上限。若普通分片先饱和，仅提高注入速率可能只增加普通交易池积压。应结合源端提交速率、Beacon 构建/验证/共识耗时及冻结队列确定瓶颈。建议先固定窗口、Beacon 间隔与冷却时间，在全聚合对照中测量多个输入档位，再对出现真实 Beacon 排队的档位成对重跑，报告全部档位。

以下人工服务成本仅用于额外的机制压力测试，必须显式指定正数，不能替代自然拥塞主实验。

experiment_work_us_per_payment 是**模拟处理容量**，不是实测 CPU 时间：每个窗口增加“聚合原交易数 × 100 微秒”的服务时间。例如 20000 笔增加 2 秒。实现为串行事件循环中的非阻塞服务门，等待时继续收取回执、处理消息和评估策略，没有 sleep。降低聚合流量可以缩短后续窗口服务时间。100us 是初始拥塞档位，并不保证每种机器/输入都有充分切换或明显收益；可固定其他参数再用 50/100/200us 做敏感性实验，完整报告所有档位。

排队时间：最老的已冻结、尚未开始服务的窗口，从冻结到当前的等待；排除当前服务/共识中的窗口。500ms 是触发阈值。10s 冷却是两次策略变化的最小间隔，首次降载不必先等待 10s。恢复还要求连续 3 次健康观测，间隔至少 1s，排队不超过 100ms，并同时满足下述交易量变化门槛。

恢复门槛：首次降载后，以连续 3 个完整 2 秒统计窗的总正金额跨片交易量建立固定基准 B（包含走 Relay2 的交易）。后续最近 3 个完整统计窗的总量 R 与其比较，只有 abs(R-B)/B 严格大于 75% 才考虑恢复。即 R 小于 0.25B 或大于 1.75B；稳定以及恰好变化 75% 均不恢复。基准与比较窗不重叠，默认最早要观察 12 秒；基准为零时不恢复。同一轮继续降载不重置基准，恢复全聚合后下一轮重新取基准。这里使用 Beacon 收到源回执的本地时间分窗，不是已完成处理量，也不是聚合比例的 75%。

可显式设置 --recovery-window-ms 2000 --recovery-windows 3 --recovery-change-percent 75；不填写即使用这些默认值。变化超过门槛仍不绕过排队健康和冷却限制。更新策略 CSV 新增 RecoveryBaselinePayments / RecoveryObservedPayments 以及窗口配置，便于复核。需要重新编译二进制并用新目录跑完整对照；旧实验结果保留，不能把旧二进制的控制组与新自适应组混成同代码对照。

## 3. 检查动态机制

运行完成会自动分析，也可重新执行：

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/analyze.py exp/你的拥塞实验目录
```

查看根目录 dynamic_validation.json / .csv：

- both_complete：两组均完成预期原交易数，且逐笔审计无重复。
- full_is_always_aggregated：全聚合对照策略版本为 0、正金额 Relay2 为 0；允许零金额交易的固定 Relay2 路径。
- switched：动态组产生策略变化。
- eligible_relay2_sent_after_switch > 0、ordinary_relay2_increased：源端在新版本下实际发送正金额普通 Relay2，且目标区块确实提交；排除零金额固定回退，避免误判为动态降载。relay2_sent_after_switch 另保留包括零金额在内的发送总数。
- relief_during_injection：仍在注入时，切换后出现至少连续 3 个采样点，其排队原交易数和最老等待均低于此前峰值。
- lower_queue_wait_area：在两组共同的注入时间范围内，动态组排队等待曲线面积更低。
- effective_evidence：上述条件同时满足；false 表示证据不足，不能直接宣称降载有效。注入结束后自然排空不单独作为有效证据。该判据是实验观测标准，不替代多次重复和容量敏感性分析。

每次运行中的关键文件：

- validation.json：完成数量、重复、Relay1/Relay2/聚合守恒和零 WARN/ERROR 审计。
- results/aggregation_routing_policy.csv：触发原因、等待、目标列表及在线统计。
- results/aggregation_queue_timeseries.csv：每次节点 tick 采样，约 500ms；QueuedOriginals 是等待服务的逻辑原交易数，PendingPayments 是尚未冻结的抽象条目数，两者不能混用。
- results/aggregation_batch_metrics.csv：ServiceQueueWaitNs 为开始服务前等待；ExperimentServiceTimeNs 为注入成本；旧 FrozenQueueWaitNs 包含开始构建前的模拟服务耗时，分析拥塞应使用前者或队列时间序列。
- queue_relative_time.csv：相对最早原交易创建时刻的队列曲线。
- source_routing_timeseries.csv：动态组源端按区块的版本和普通 Relay2 发送数。
- summary.json：原交易 TPS、平均/P95/P99 时延等。
- manifest.json、config.yaml、ip_table.json：可复核配置、数据及二进制哈希。
- block_record/、logs/、process_logs/：原始证据。

任何计数不守恒、完成数不足或 WARN/ERROR 都使该次运行失败；策略未触发或拥塞收益不足会如实标记，不伪装为成功。性能比较必须使用此次重跑结果；旧静态 75%/100% 的源端路径不同，不能混入新代码主表。

## 4. 小规模功能检查

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_suite.py congestion \
  --shards 4 --tx-number 4000 --per-shard-rate 50 \
  --beacon-work-us 8000 --cooldown-ms 1000 --repeats 1 \
  --output "exp/newcode-smoke-$(date +%Y%m%d-%H%M%S)"
```

这是验证链路的强拥塞档位与短冷却，不能把结果当作 24 分片性能数据。默认终止超时为每次 1800 秒，可用 --timeout 调整。

单次运行也可用 run.py --mode full/adaptive/monoxide/brokerchain/clpa；--bins 可复用二进制。不同源码版本的二进制不可混合合并统计。节点已启动后失败的半成品目录应保留，另开目录重跑；如果只是启动前的端口检查失败，可使用下述安全续跑功能。

### 启动前失败后续跑

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/resume.py "exp/原套件目录"
```

续跑读取原 plan.json 和 manifest.json，核对数据与二进制哈希、原配置及当前配置生成规则。已完成且审计通过的组不重跑；只含启动配置、失败说明和空 process_logs/ 的失败目录会改名为 *.failed-startup-时间戳保留，再使用原参数和原二进制重试该组。若发现已启动节点的输出、数据变化或配置不匹配，会拒绝续跑。完成后更新根目录比较汇总与动态验证报告。此操作不会杀其他进程或覆盖已完成的组。

macOS 上一组退出后的 TCP TIME_WAIT 不代表仍有节点监听。端口预检查采用 SO_REUSEADDR 加实际 listen() 验证，匹配 Go 监听器的复用行为；真正的监听冲突仍会阻止启动，不自动切换端口或终止其他程序。

本次实际功能验证输出为 exp/congestion-smoke-verified：上述 4×4/4000 笔成对配置，两组均全部完成、无重复和 WARN/ERROR。full 的 Relay2=0，adaptive 的 Relay2=1197、策略版本数=5；注入期间缓解判据通过，等待曲线面积分别为 32.9144 / 24.1309 秒²。此结果只验证脚本与动态机制链路，不代表 24 分片性能。

## 5. 原始大数据集与百万笔自然高负载

当前项目默认 CSV 是 141584503 字节、500000 行原始记录，不应把它当成完整归档。更大的同名 CSV 位于：

```text
/Users/ljn/Desktop/Newidea2026July/block-emulator-x-netting-dev_多边净额2.0/data/25250000to25499999_BlockTransaction.csv
```

该文件大小为 21536935836 字节。2026-10-27 使用 cmd/selectbrokers 内部实际 CSVSource（排除合约交易）成功读出了 2000000 笔有效交易，确认能支持以下实验；未扫描统计整个文件的有效总数。对应压缩归档保留在 /Users/ljn/Downloads/25250000to25499999_BlockTransaction.zip。不要把当前小文件与大文件直接拼接，也不要把重复播放称为新增真实交易。

可直接指定原始文件，不必覆盖本项目默认数据：

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run_suite.py congestion \
  --dataset "/Users/ljn/Desktop/Newidea2026July/block-emulator-x-netting-dev_多边净额2.0/data/25250000to25499999_BlockTransaction.csv" \
  --shards 24 --tx-number 2000000 --per-shard-rate 1200 \
  --block-limit 8000 --block-interval-ms 2000 \
  --beacon-interval-ms 1000 --window-ms 2000 \
  --freeze-high-ms 500 --freeze-low-ms 100 --cooldown-ms 10000 \
  --beacon-work-us 0 --repeats 1 --timeout 3600 \
  --output "exp/natural-24-2m-rate1200-$(date +%Y%m%d-%H%M%S)"
```

这会运行 full/adaptive 各一次、每组 200 万笔。名义总注入速率 28800 tx/s，名义注入持续约 69.4 秒；实际注入过程还受节点和 Supervisor 调度影响。相比 30 万笔，它为多次 10 秒冷却与后续缓解观测留出更多时间。可以固定其余配置，再测每分片 600 / 1200 / 2400 的档位，输出目录分别命名，正式结果使用 --repeats 3。8000 是普通分片区块上限，不能解释为 Beacon 每块处理上限。这些档位尚未完成真实高负载验证，不保证触发切换；以队列、Relay2、完成性及 dynamic_validation.json 的实际证据为准。

此处更换的是数据源及工作负载规模，不能将其性能直接归因于路由策略并与原 30 万笔小文件结果混合比较。两组必须使用同一个新数据源和相同有效交易前缀。

## 6. 原始数据的零金额交易兼容性修复

2026-10-28 的 24 分片百万笔运行在源区块提交后报 invalid finalized Relay1 envelope。原始大 CSV 的前 100 万笔有效非合约交易含 21950 笔零金额交易，而本项目默认小 CSV 中没有零金额交易；此前 CSV 读取器允许零金额，但聚合付款要求正金额，导致已提交源块无法完成回执交付。

修复不筛除输入交易，也不放宽聚合资金校验：零金额跨片交易保持普通封套，在源块提交后固定走 Relay2；正金额交易按策略正常分流。零金额交易不加入 Beacon 聚合付款与目的分片收益评分，零金额源块仍发布回执，保留连续水位。nil/负金额仍拒绝，错误日志包含源分片、块高、原交易 ID 和金额。

block_record.csv 新增 ZeroValueRelay1TxCount / ZeroValueRelay2TxCount。审计要求零金额源/目标计数一致；全聚合对照允许且仅允许零金额 Relay2，eligible_relay2_count 是正金额 Relay2 数。动态机制报告也排除这条固定回退路径。旧结果没有新列时按零处理，旧小数据实验结论不变。

完整 Go 测试覆盖混合正/零金额、全零源块水位、非法金额、计数与完成性审计。另对原始大 CSV 的前 19200 笔交易做了离线 24 分片路由和聚合批次证明校验：18331 笔跨片交易 = 18064 笔聚合 + 267 笔零金额 Relay2；输入中的零金额交易共 296 笔（其余为片内）。该离线验证不等同于百万笔多节点重跑。

已报错的运行不能通过编辑源码恢复。停止旧运行后，用新输出目录重新执行原百万笔命令；不要添加 --bins 指向旧二进制，套件会重新编译修复后的源码。历史日志和结果保留。本次修复没有启动新实验或停止现有进程。

支付实验的最终口径：零金额不属于有效支付样本，在输入端统一跳过。本节前述 Relay2 回退保留为普通 CSV/手工注入的兼容性处理，不是实验的取样策略。新的 100 万笔任务意味着实际注入并完成 100 万笔正金额非合约交易，而非先读 100 万笔再减去零金额；包括 Monoxide、BrokerChain、CLPA、full、adaptive 的所有配置使用一致规则。通用 CSV 接口未启用 positive_value_only 时仍可读取零金额，避免改变其他非支付用途。新增配置需要重新编译的二进制，不能复用忽略此字段的旧程序。
