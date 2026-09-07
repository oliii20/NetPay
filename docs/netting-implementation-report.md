# BlockEmulator-X Netting 论文实现实验文档

支付意图、资金预留、批量撮合、Beacon MatchRoot 与分片内清算实现说明

生成日期：2026-09-07  
仓库：`/Users/ljn/Desktop/Newidea2026July/block-emulator-x-netting-dev`  
分支：`feat/block-netting`；基线提交：`a5635e7`；当前提交：`9f9751a`

## 1. 实现总览

本文档记录在 BlockEmulator-X 上实现“基于支付意图、资金预留、链下批量撮合、Beacon MatchRoot 确认、分片内并行清算与 fallback”的论文 idea 时，对原始代码所做的主要修改。

对比基线为原始仓库在 netting 分支开始前的提交 a5635e7；当前实现提交为 9f9751a，分支为 feat/block-netting。总体差异约为 97 个文件，新增约 11800 行，删除约 69 行。

实现原则是尽量不破坏 BlockEmulator-X 原有 static_relay / static_broker 路径，而是在 config.netting.enabled 打开时把 netting 作为一条可选协议路径挂到原有 PBFT、txblockop、supervisor 和 metric 框架上。

1. 用户交易输入后，Supervisor 将跨分片 NormalTx 转换为 PaymentIntent，并发送到源分片。
2. 源分片在普通区块共识过程中执行 intent 预留，写入 registry，并在区块 finalized 后向 Solver 发布 receipt。
3. Solver 根据 receipt 维护异步 Vector-Cut 窗口，满足 BatchSize 或 MaxWindowDuration 后冻结窗口。
4. Solver 对窗口内 intent 执行 exact → best-fit → split 撮合，生成 batch sidecar、settlement package 和 MatchRoot。
5. Beacon 节点验证 batch 可由 receipt 和 matcher mode 确定性派生，再通过 PBFT commit MatchRoot。
6. 普通分片收到 MatchRootFinalized 和 SettlementPackage 后，本地验证 Merkle proof，执行 reserved balance 消耗和收款人入账。
7. 未净额化的剩余金额进入 ReservedFallback 路径，继续走保留资金驱动的普通跨分片回退流程。
8. Supervisor 与 netting metric collector 收集 batch、intent、Beacon、execution 指标，供实验脚本汇总并画图。

## 2. 分阶段实现路线

| 阶段 | 主题 | 主要文件 | 作用 |
| --- | --- | --- | --- |
| 1 | 三阶段撮合算法 | pkg/netting/matcher/* | 实现 exact、best-fit、split 的确定性双向净额撮合。 |
| 2 | 异步窗口与 Vector-Cut | pkg/netting/window/* | 用 BatchSize 和 MaxWindowDuration 关闭窗口，并冻结各分片连续水位。 |
| 3 | 资金预留与 Escrow | pkg/netting/registry/*; pkg/chain/intent.go | 在源分片验证 intent 后锁定 reserved balance，防止撮合期间重复花费。 |
| 4 | 源分片 receipt 发布 | pkg/netting/receipt/*; txblockop/* | 普通分片在区块确认后向 Solver 发布已预留 intent 的区块确认信息。 |
| 5 | Solver 批处理 | pkg/netting/solver/*; pkg/netting/batch/* | 收集 receipt、关闭窗口、构造 MatchRoot 与 settlement sidecar。 |
| 6 | Beacon Chain 批量确认 | cmd/beaconnode/*; consensus/pbft/insideop/beaconop/*; pkg/netting/beacon/* | 把大量 intent 的撮合结果压缩为一次 MatchRoot 共识确认。 |
| 7 | 分片内 settlement | pkg/netting/settlement/*; pkg/chain/settlement.go | 各分片验证 MatchRoot/Merkle proof 后执行本地扣款和入账。 |
| 8 | Fallback 回退 | pkg/netting/fallback/*; pkg/chain/fallback.go | 未净额化金额释放为保留资金驱动的普通跨分片回退交易。 |
| 9 | 指标系统 | pkg/netting/metrics/*; supervisor/measure/nettingstats/* | 记录 batch、Beacon、intent 生命周期、锁定时间、证明验证时间等论文指标。 |
| 10 | 端到端运行脚本 | example_run_netting.sh; config.netting-e2e.yaml; ip_table_netting.json | 提供 4×4 本地复现实验入口。 |
| 11 | 论文实验与画图 | scripts/netting_experiments/*; figures/netting/* | 实现 1–7 组实验、汇总 summary.csv，并输出科研风格 SVG 图。 |

## 3. 相对原始 BlockEmulator-X 的文件修改清单

| 模块 | 涉及文件 | 做了什么 | 目的 |
| --- | --- | --- | --- |
| 配置与入口 | config/config.go; config/config_consensus.go; config.yaml; config.netting-e2e.yaml | 新增 netting 配置段：enabled、metrics_enabled、batch_size、max_window_duration_ms、solver_tick_interval_ms、matcher_mode、batch/beacon store path；新增异步实验用 shard_block_intervals_ms。 | 让同一个 BlockEmulator-X 仓库可以在原始 relay/broker 与 netting 机制之间切换，并支持参数扫描实验。 |
| 独立进程入口 | cmd/solver/main.go; cmd/beaconnode/main.go | 新增 Solver 进程和 Beacon 节点进程启动入口，读取配置、加载网络拓扑、初始化存储并启动事件循环。 | 把链下求解器与 Beacon 确认链显式建模，而不是塞进 supervisor 的统计逻辑里。 |
| 网络拓扑 | pkg/nodetopo/topology.go; pkg/nodetopo/topogetter.go; ip_table_netting.json | 扩展 topology，加入 Solver shard、Beacon shard、Supervisor 的路由解析。 | 让普通分片、Solver、Beacon、Supervisor 之间都能用原有 direct RPC 通信框架互相发送协议消息。 |
| RPC 延迟注入 | cmd/loadnetwork/loadnetwork.go; pkg/network/clientconnrpc/rpcconn.go | direct RPC 连接新增可配置 latency，非本地消息发送前按配置延迟。 | 支撑第 7 组异步/网络延迟实验，观察 Vector-Cut skew、fallback 比例和端到端延迟。 |
| 交易与 intent 类型 | pkg/core/intent/intent.go; pkg/core/transaction/transaction.go | 新增 PaymentIntent 数据结构，以及 IntentSubmit、Settlement、ReservedFallback、FallbackCompleted 等系统交易类型。 | 把用户跨分片支付从普通交易抽象为可预留、可撮合、可证明、可回退的 intent 生命周期。 |
| 链上预留 | pkg/chain/intent.go; pkg/netting/registry/registry.go; pkg/chain/stateio.go | 实现 source shard 验证 intent、nonce、余额并写入 reservation registry；记录 matched/fallback/status/batch。 | 保证资金在撮合窗口内不能被重复使用，是方案安全性的链上根。 |
| 撮合算法 | pkg/netting/matcher/matcher.go | 实现 exact-only、best-fit、full 三种模式；full 模式执行 exact → best-fit → split。 | 对应论文提出的实用撮合顺序，并为第 5 组消融实验提供开关。 |
| Vector-Cut 窗口 | pkg/netting/window/manager.go; pkg/netting/window/state.go | 按 receipt 连续水位维护 open/frozen window；满足 BatchSize 或 MaxWindowDuration 即关闭窗口。 | 避免依赖全局区块高度，使异步分片网络也能稳定工作。 |
| 批次构造与 Merkle 证明 | pkg/netting/batch/builder.go; pkg/netting/model/batch.go; pkg/netting/model/result.go | 从 frozen window 构造 intent results、shard settlements、CutRoot、IntentResultRoot、ShardSettlementRoot、MatchRoot 和 BatchID；header 写入 MatcherMode。 | 让 Beacon 只共识 MatchRoot，同时各分片可以用 proof 独立验证自己要执行的 settlement。 |
| Solver 状态机 | pkg/netting/solver/node.go; pkg/netting/batchstore/store.go | Solver 收集 receipt、恢复窗口状态、构造 proposal、持久化 sidecar，并确保同一 pending batch 只提交一次。 | 实现链下撮合器的可恢复批处理逻辑，避免 Beacon 重复 proposal 造成序列错误。 |
| Beacon 验证与共识接入 | pkg/netting/beacon/validator.go; pkg/netting/beacon/store.go; consensus/pbft/insideop/beaconop/beaconop.go | Beacon 验证 batch sequence、Vector-Cut 覆盖、intent 未重复、未过期、以及按 MatcherMode 重新确定性派生结果；PBFT commit 后广播 MatchRootFinalized。 | 把大量跨片支付的一致性确认压缩成一次 Beacon 共识。 |
| 分片外部消息处理 | consensus/pbft/outsideop/netting.go | 普通分片处理 MatchRootFinalized、SettlementPackage、FallbackTx、FallbackCompleted 等跨角色消息。 | 让分片能在收到全局确认和 proof 后进入本地 settlement/fallback 执行。 |
| 普通区块执行接入 | consensus/pbft/node.go; consensus/pbft/insideop/txblockop/relaytxblockop.go; consensus/pbft/insideop/txblockop/brokertxblockop.go | 在 PBFT 节点构造时按 netting.enabled 包装 inside/outside op；出块后发布 receipt、fallback、execution metrics，并过滤 netting 系统交易对 legacy 统计的干扰。 | 在不破坏原始 static_relay/static_broker 的前提下挂接新协议。 |
| Settlement 执行 | pkg/netting/settlement/executor.go; pkg/netting/settlement/inbox.go; pkg/chain/settlement.go | 维护 MatchRoot 确认和 settlement package inbox；验证 package proof，消耗 reserved balance，给本分片收款人入账，生成 fallback outbox。 | 把匹配成功的双向跨片支付转化为两个分片各自的本地清算。 |
| Fallback 执行 | pkg/netting/fallback/outbox.go; pkg/netting/fallback/executor.go; pkg/netting/fallback/publisher.go; pkg/chain/fallback.go | 为未净额化剩余金额生成 ReservedFallback，目标分片执行普通入账，源分片收到完成消息后标记 completion。 | 保证无法净额化的金额仍然可回退到原有跨分片完成路径。 |
| 消息协议 | pkg/message/nettingmsg.go; pkg/message/message.go; pkg/message/pbftmsg.go | 新增 FinalizedBlockReceipt、BatchProposal、MatchRootFinalized、SettlementPackage、FallbackTx、FallbackCompleted、NettingMetric、NettingProgress 等消息和 PBFT proposal 包装。 | 让 netting 的链上/链下角色复用原有 WrappedMsg 与 PBFT 消息通道。 |
| Supervisor workload | supervisor/committee/netting.go; supervisor/committee/staticrelay.go; supervisor/committee/staticbroker.go | Supervisor 将跨分片 NormalTx 转为 PaymentIntent，并通过 progress、fallback completion、execution metric 判断 netting 工作是否完成。 | 让实验可以用原始数据集驱动 netting 机制，并让 supervisor 正确终止。 |
| 指标收集 | pkg/netting/metrics/publisher.go; supervisor/measure/router.go; supervisor/measure/nettingstats/nettingstats.go | 将 batch、Beacon、execution 三类指标路由到 netting collector；输出 netting_batch_metrics.csv 与 netting_intent_metrics.csv。 | 支撑论文实验中的吞吐、延迟、matched/fallback ratio、Beacon overhead、proof time、capital lock time 等指标。 |
| 实验与画图 | scripts/netting_experiments/run_experiments.py; scripts/netting_experiments/plot_experiments.py; docs/netting-experiments.md; figures/netting/*.svg | 实现 baseline、净额化收益、BatchSize、MaxWindowDuration、算法消融、系统规模、异步/网络延迟 7 组实验，并生成论文风格矢量图。 | 让论文实验从运行、汇总到可视化形成可重复流水线。 |
| 测试文件 | pkg/netting/**/*_test.go; pkg/chain/*_test.go; supervisor/**/*_test.go; consensus/**/*_test.go; pkg/message/*_test.go | 为 matcher、window、batch、Beacon、Solver、settlement、fallback、metrics、supervisor 路由等模块增加单元/端到端测试。 | 降低大改动引入隐蔽状态机错误的风险。 |

## 4. 7 组论文实验代码与图

| 实验 | 自变量/方法 | 指标 | 论文中回答的问题 |
| --- | --- | --- | --- |
| 1 Baseline | static_relay、static_broker、netting_static_relay | throughput、latency、cross_messages_per_tx、Beacon/confirmation cost | 展示你的机制是否减少逐笔跨片消息和目标分片处理。 |
| 2 Netting benefit | reverse_ratio = 0%,25%,50%,75%,100% | matched_value_ratio、fallback_value_ratio | 证明双向流量越强，净额化收益越高。 |
| 3 BatchSize | 10,20,50,100,200 | throughput、beacon_bytes_per_intent、capital_lock_s、avg_latency_s | 展示批量摊销收益与等待/锁资成本的权衡。 |
| 4 MaxWindowDuration | 100ms,500ms,1s,2s,5s | matched_value_ratio、avg_latency_s | 展示撮合窗口越长，撮合率和端到端延迟之间的 trade-off。 |
| 5 Matcher ablation | exact_only、best_fit、full | matched/fallback ratio、split_allocations | 证明三阶段策略相对于简单 exact-only 的价值。 |
| 6 Scale | shard_num = 4,8,16; node_num = 4 | throughput、beacon overhead、match_time_ms、proof_time_ms | 展示系统规模扩展时 Solver/Beacon/分片验证开销。 |
| 7 Async/network latency | network_latency_ms sweep + heterogeneous shard block intervals | avg_latency_s、watermark_skew、fallback_value_ratio | 验证异步窗口规则在无全局高度条件下仍稳定。 |

## 5. 验证记录

| 验证项 | 命令 | 结果/说明 |
| --- | --- | --- |
| Go build | GOCACHE=$PWD/.exp/gocache go build ./... | 通过。沙箱内默认 Go cache 不可写，使用仓库内 GOCACHE。 |
| Go tests | GOCACHE=$PWD/.exp/gocache go test -gcflags=all='-N -l' ./... | 通过，覆盖 chain、PBFT、netting、supervisor、network 等包。 |
| Python syntax | PYTHONPYCACHEPREFIX=$PWD/.exp/pycache python3 -m py_compile scripts/netting_experiments/*.py | 通过。 |
| Experiment smoke | scripts/netting_experiments/run_experiments.py --profile smoke | 第 5 组消融 smoke 已实际跑通；完整 1–7 组真实多进程 smoke 曾因 Codex 使用额度/外部进程限制中断，脚本已可由本地终端继续运行。 |
| Plot guard | python3 scripts/netting_experiments/plot_experiments.py | 默认要求 summary.csv 包含 1–7 组，避免不完整数据生成论文图；调试可用 --allow-missing。 |

## 6. 推荐代码阅读顺序

| 顺序 | 阅读目标 | 文件 | 读懂什么 |
| --- | --- | --- | --- |
| 0 | 先看总览文档 | docs/netting-implementation-report.md; docs/netting-e2e.md; docs/netting-metrics.md; docs/netting-experiments.md | 先建立概念地图，知道协议角色、指标边界和实验入口。 |
| 1 | 看配置如何打开 netting | config/config.go; config/config_consensus.go; config.netting-e2e.yaml | 理解 enabled、batch_size、MaxWindowDuration、matcher_mode、store path 等实验开关。 |
| 2 | 看数据结构 | pkg/core/intent/intent.go; pkg/core/transaction/transaction.go; pkg/message/nettingmsg.go; pkg/netting/model/*.go | 先认识 PaymentIntent、IntentResult、MatchRootBlockBody、SettlementPackage、FallbackKey 等核心对象。 |
| 3 | 看源分片预留 | pkg/chain/intent.go; pkg/netting/registry/registry.go | 理解 source shard 如何验证余额/nonce、写入 reserved 状态。 |
| 4 | 看 receipt 和窗口 | pkg/netting/receipt/receipt.go; pkg/netting/window/manager.go; pkg/netting/window/state.go | 理解为什么异步网络不用全局高度，而用各分片连续水位冻结 Vector-Cut。 |
| 5 | 看撮合算法 | pkg/netting/matcher/matcher.go | 按 exact-only → best-fit → split 的顺序读，这是论文算法贡献的核心。 |
| 6 | 看 batch 构造 | pkg/netting/batch/builder.go | 理解如何从 frozen window 生成 intent result、分片 settlement 和三层 Merkle commitment。 |
| 7 | 看 Solver | pkg/netting/solver/node.go; pkg/netting/batchstore/store.go; cmd/solver/main.go | 理解链下求解器如何收集、关窗、构造批次、提交 Beacon，并避免重复 proposal。 |
| 8 | 看 Beacon | pkg/netting/beacon/validator.go; pkg/netting/beacon/store.go; consensus/pbft/insideop/beaconop/beaconop.go; cmd/beaconnode/main.go | 理解 MatchRoot 如何被全局确认，以及 Beacon 如何重新派生结果做确定性验证。 |
| 9 | 看 settlement/fallback | pkg/netting/settlement/*.go; pkg/chain/settlement.go; pkg/netting/fallback/*.go; pkg/chain/fallback.go | 理解 confirmed MatchRoot 后各分片如何本地清算，剩余金额如何进入回退路径。 |
| 10 | 看 PBFT 接线 | consensus/pbft/node.go; consensus/pbft/outsideop/netting.go; consensus/pbft/insideop/txblockop/*.go | 理解 netting 如何嵌入原始 BlockEmulator-X 的单线程 PBFT 出块流程。 |
| 11 | 看 supervisor 和指标 | supervisor/committee/netting.go; supervisor/measure/router.go; supervisor/measure/nettingstats/nettingstats.go; pkg/netting/metrics/publisher.go | 理解实验输入如何从 CSV 转 intent，结果如何输出为论文指标。 |
| 12 | 最后看实验脚本和图 | scripts/netting_experiments/run_experiments.py; scripts/netting_experiments/plot_experiments.py; figures/netting/*.svg | 理解 7 组实验如何配置、运行、汇总和画图。 |
| 13 | 用测试反向理解 | pkg/netting/matcher/matcher_test.go; pkg/netting/window/manager_test.go; pkg/chain/netting_e2e_test.go; consensus/pbft/insideop/beaconop/beaconop_test.go; supervisor/measure/nettingstats/nettingstats_test.go | 测试通常比实现更短，适合确认每个模块的输入输出语义。 |

## 7. 进一步写论文时可强调的实现边界

- 当前实现先不考虑用户签名和拜占庭求解器安全实验；PaymentIntent 的签名字段和恶意 Solver 检测可作为后续安全性实验扩展。
- 当前窗口关闭规则保留 BatchSize 与 MaxWindowDuration，不依赖全局区块高度；Vector-Cut 由各分片连续 finalized receipt 冻结，因此可用于异步分片网络。
- 第 2–7 组实验主要使用受控 synthetic workload，以隔离变量；第 1 组 baseline 使用 selectedTxs_300K.csv 的真实 trace 切片。
- cross_messages_per_tx 是协议级估算，排除了 TCP/IP、gRPC、protobuf framing；Beacon bytes 和 proof time 来自 netting metrics 路径。
