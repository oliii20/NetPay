# BlockEmulator-X 异步批量净额清算设计

- 状态：已由用户确认
- 日期：2026-09-03
- 实现分支：`feat/block-netting`
- 基线：`origin/main` at `8db5311`

## 1. 目标

在 BlockEmulator-X 上实现一种跨分片支付意图批量净额清算协议：

1. 用户的跨分片支付首先作为支付意图进入源分片，随源分片正常 PBFT 区块完成资金预留。
2. 独立 Solver 从各源分片收集已经最终确认的区块回执，以不要求分片高度同步的 Vector-Cut 方式形成窗口。
3. Solver 对窗口内支付按照“分片对—资产”分组，执行确定性的完全相等优先、Best-Fit、无法单笔覆盖时优先消耗大额交易的撮合算法。
4. Solver 生成每笔 Intent 的匹配金额、回退金额、分片执行指令和 Merkle 承诺，将批次提交给独立的四节点 Beacon PBFT。
5. Beacon 验证窗口、预留回执、金额守恒和 Merkle 承诺，并只在链上持久化批次根和 Vector-Cut 边界。
6. MatchRoot 最终确认后，各普通分片通过自己的正常 PBFT 区块独立执行本地清算；未匹配金额从 Escrow 直接进入目标分片的 fallback 入账阶段。

本设计优先保证协议状态清晰、执行确定、金额守恒、消息幂等以及 Git 修改可审查。

## 2. 第一版范围

### 2.1 包含

- 静态分片模式。
- 单一原生资产。
- 全局 BatchSize。
- MaxWindowDuration。
- 异步 Vector-Cut 窗口。
- 一个 Intent 只参加一个窗口和一个批次。
- 独立 Solver、独立 Beacon PBFT，每个 Beacon 委员会四个节点。
- 完全相等优先、Best-Fit、largest-first split。
- 两级分片清算 Merkle 承诺。
- Settlement Chunk。
- 基于 Escrow 的资金预留。
- 从预留资金直接产生 fallback。
- Solver、Beacon sidecar 和清算指标。

### 2.2 不包含

- 用户签名验证。
- 带恶意节点的 Byzantine 安全实验。
- PBFT vote 和 finalized receipt 的密码学证书。
- CLPA 动态账户迁移。
- 多资产余额执行。
- 一个 Intent 跨多个窗口重复部分匹配。
- 用户主动取消。
- Solver、Beacon 永久失效后的自动退款协议。
- 纠删码或独立数据可用性网络。

`PaymentIntent.Signature` 字段保留用于后续扩展，但第一版不参与验证，也不参与 Intent ID 计算。

## 3. 系统架构

系统包含四类角色：

1. **Supervisor**：生成工作负载、控制实验、收集指标；不承担最终论文系统中的 Beacon 职责。
2. **普通分片节点**：运行现有 PBFT，处理 IntentSubmit、SettlementChunk 和 fallback 入账。
3. **Solver**：收集最终区块回执、推进分片水位、关闭窗口、撮合、生成 Merkle Tree 和 BatchProposal。
4. **Beacon 节点**：作为特殊服务分片运行独立 PBFT，验证 BatchProposal 并确认 MatchRoot。

主数据流：

```text
Supervisor
    │ IntentSubmit
    ▼
源分片 PBFT ── FinalizedBlockReceipt ──► Solver
    │                                      │
    │                                      ├─ Vector-Cut window
    │                                      ├─ deterministic matcher
    │                                      └─ Merkle batch
    │                                               │
    │                                               ▼
    │                                      Beacon PBFT
    │                                               │ MatchRootFinalized
    ▼                                               ▼
普通分片本地 Settlement PBFT ◄──────── SettlementPackage
    ├─ matched: Escrow → 本分片原始接收人
    └─ fallback: Escrow → ReservedFallback → 目标分片 PBFT
```

“并行清算”指不同普通分片并行运行各自的 PBFT 和状态转换；单个 BlockEmulator-X 节点继续保持串行执行模型。

## 4. 实施阶段

实现采用分层内核优先方式：

1. Intent 数据模型和确定性哈希。
2. Merkle Tree 和 proof。
3. 确定性撮合器。
4. Vector-Cut 窗口。
5. StateDB Escrow 和 Reservation。
6. 普通分片 finalized receipt。
7. Solver 网络进程和 BatchStore。
8. Beacon PBFT 和 MatchRootBlock。
9. 普通分片 Settlement Chunk。
10. Reserved fallback。
11. 指标和端到端实验。

每个阶段先产生未提交 diff，解释修改并运行测试；用户审查后才创建对应 Git commit。

## 5. 支付意图

新增 `pkg/core/intent`：

```go
type ID [32]byte
type AssetID [32]byte

var NativeAssetID AssetID

type PaymentIntent struct {
    Version          uint8
    ChainID          uint64
    Sender           account.Address
    Recipient        account.Address
    SourceShard      int64
    DestinationShard int64
    AssetID          AssetID
    Amount           *big.Int
    Nonce            uint64
    ExpiryEpoch      uint64
    Signature        []byte
}
```

第一版要求：

- `AssetID == NativeAssetID`。
- `Amount > 0`。
- Sender 与 Recipient 不同。
- SourceShard 和 DestinationShard 合法且不同。
- SourceShard 等于当前执行分片。
- Intent 尚未过期。

Intent ID 使用 SHA-256 和确定性二进制编码：

```text
IntentID = SHA256(
    "BLOCKEMULATOR_NETTING_INTENT_V1"
    || Version
    || ChainID
    || Sender
    || Recipient
    || SourceShard
    || DestinationShard
    || AssetID
    || Amount
    || Nonce
    || ExpiryEpoch
)
```

规则：

- 整数采用大端编码。
- 地址固定 20 字节。
- Hash 和 AssetID 固定 32 字节。
- 金额采用无符号、无前导零、带长度前缀的编码。
- Signature、CreateTime、Gob 输出不参与 ID。

## 6. Transaction 兼容

保留现有 Normal、Relay、Broker 和合约交易逻辑，只新增：

```go
const (
    IntentSubmitTxType byte = 5
    SettlementTxType   byte = 6
)

type IntentTxOpt struct {
    Intent *intent.PaymentIntent
}

type SettlementTxOpt struct {
    Settlement *settlement.ShardSettlement
}
```

`Transaction.TxType()` 优先识别 IntentSubmit 和 Settlement，旧交易类型保持原有推断行为。新增类型不能通过已有 Relay/Broker 可选字段被误判。

## 7. Reservation 与 Escrow

### 7.1 系统地址

每个普通分片使用相同的固定系统地址：

```text
EscrowAccountAddress =
last20(SHA256("BLOCKEMULATOR_NETTING_ESCROW_V1"))

IntentRegistryAddress =
last20(SHA256("BLOCKEMULATOR_NETTING_REGISTRY_V1"))
```

Escrow 原生余额存入 go-ethereum StateDB。Reservation 元数据存入 IntentRegistryAddress 的 storage slots。因此它们统一受到普通分片 StateRoot 承诺，不新增第四套链上状态数据库。

### 7.2 状态

```go
type ReservationStatus uint8

const (
    ReservationUnknown ReservationStatus = iota
    ReservationReserved
    ReservationConsumed
    ReservationExpired
)

type Reservation struct {
    IntentID       intent.ID
    Sender         account.Address
    Recipient      account.Address
    Amount         *big.Int
    MatchedAmount  *big.Int
    FallbackAmount *big.Int
    Nonce          uint64
    ExpiryEpoch    uint64
    BatchID        [32]byte
    Status         ReservationStatus
}
```

第一版正常路径：

```text
Unknown → Reserved → Consumed
```

### 7.3 storage slot

```text
H("intent:amount"   || IntentID) → reserved amount
H("intent:matched"  || IntentID) → matched amount
H("intent:fallback" || IntentID) → fallback amount
H("intent:batch"    || IntentID) → BatchID
H("intent:status"   || IntentID) → status
H("intent:nonce"    || Sender)   → next Intent nonce
```

完整 PaymentIntent 存在源分片区块 body 和 finalized receipt 中。清算阶段重新计算 Intent ID，并以该 ID 查询 Registry，验证 Solver 携带的 PaymentIntent 没有被修改。

### 7.4 IntentSubmit 执行

执行顺序：

1. 解析 Intent 并重算 Intent ID。
2. 验证第一版静态规则。
3. 检查 Registry 中不存在 Intent ID。
4. 检查 `Intent.Nonce == NextIntentNonce(Sender)`。
5. 检查发送方余额。
6. 发送方原生余额减去 Amount。
7. 本分片 Escrow 原生余额增加 Amount。
8. 写入 ReservationReserved。
9. Intent nonce 加一。

第一版采用独立 Intent nonce，避免改变现有普通交易、Relay、Broker 和历史数据集的 nonce 语义。签名阶段再评估是否统一为账户 nonce。

### 7.5 无效候选交易

Netting 模式的 leader 在正式构造 Proposal 前，以 StateDB snapshot 顺序试执行候选交易：

- 合法交易进入区块。
- 非法 Intent 被拒绝并记录原因。
- 单笔非法 Intent 不拖垮其他合法交易。
- 非法 Intent 不无限放回 TxPool。

Follower 对正式 Proposal 完整重执行并核对 StateRoot。`ValidateBlock` 必须补齐父哈希、高度、区块类型、StateRoot 和 LocationRoot 验证；`AddBlock` 必须核对实际提交根。

## 8. FinalizedBlockReceipt

每个普通分片 leader 在每个业务区块提交后发送：

```go
type FinalizedBlockReceipt struct {
    ShardID    int64
    Height     uint64
    BlockHash  [32]byte
    ParentHash [32]byte
    StateRoot  [32]byte
    Epoch      int64
    Intents    []PaymentIntent
    CommitTime time.Time
}
```

即使区块没有 Intent，也发送空回执。第一版只接受拓扑中普通分片 node 0 的回执，不验证密码学最终性证书。

## 9. 异步 Vector-Cut 窗口

### 9.1 连续水位

Solver 为每个普通分片维护：

```go
type ShardStream struct {
    NextExpectedHeight uint64
    ContinuousHeight   uint64
    BufferedBlocks     map[uint64]FinalizedBlockReceipt
}
```

只有从 NextExpectedHeight 开始连续收到的区块才能推进 ContinuousHeight。未来高度的乱序回执先缓存；单个分片出现缺口不会阻塞其他分片。

### 9.2 eligible Intent

Intent 满足以下条件后计入当前窗口：

- 所在区块已经成为该分片连续 finalized receipt 前缀的一部分。
- Intent ID 尚未分配给任何已封存窗口。
- Receipt 中的 Intent 在源分片声称为 Reserved。

### 9.3 窗口启动

窗口在第一个 eligible Intent 出现时设置 `OpenedAt`。空区块不会启动计时，没有 pending Intent 时不会生成空批次。Clock 通过接口注入，测试使用 FakeClock。

### 9.4 两个关闭条件

```text
sizeReady = PendingIntentCount >= BatchSize

timeReady = PendingIntentCount > 0
    && Now - OpenedAt >= MaxWindowDuration

ShouldClose = sizeReady || timeReady
```

BatchSize 统计所有普通分片、所有方向的全局 Intent 数量。它是关闭阈值而不是严格上限，因为 Vector-Cut 必须在完整区块边界关闭。

### 9.5 封存边界

窗口关闭时执行 `SealWindowAtCurrentWatermarks()`：

```go
type ShardCut struct {
    ShardID        int64
    PreviousHeight uint64
    EndHeight      uint64
    EndBlockHash   [32]byte
}
```

对每个分片：

```text
PreviousHeight = LastAssignedHeight[shard]
EndHeight      = ContinuousHeight[shard]
```

窗口包含 `(PreviousHeight, EndHeight]`。未推进的慢分片允许 PreviousHeight 等于 EndHeight，因此不会阻塞其他分片。

封存只固定 Solver 的读取边界，不暂停分片、不锁住区块链。封存后到达的区块进入下一个窗口。

### 9.6 流水线和恢复

窗口封存后立即开始收集下一窗口。FrozenWindow 可以排队，但向 Beacon 提交时严格按 WindowID 顺序。Solver 持久化：

- LastAssignedHeight。
- 每分片 ContinuousHeight。
- BufferedBlocks。
- FrozenWindow 队列。
- 已提交但尚未确认的批次。

## 10. 撮合算法

### 10.1 分组

```go
type GroupKey struct {
    LowerShard  int64
    HigherShard int64
    AssetID     intent.AssetID
}
```

每组分成 Lower→Higher 和 Higher→Lower 两个方向。

### 10.2 完全相等优先

两个方向分别建立 `amount → IntentID 有序队列`。金额相同的 Intent 成对完整匹配；同金额时按 Intent ID 字典序。

### 10.3 Best-Fit

精确匹配后重新计算两个方向剩余总额。总额较小方向作为主动方向；总额相等时 Lower→Higher 为主动方向。

主动方向按剩余金额降序、Intent ID 升序处理。反方向使用按 `(remaining amount, IntentID)` 排序的有序多重集合。对主动 Intent x，执行 LowerBound，寻找 `remaining >= x.remaining` 的最小 Intent y。

第一版使用 `github.com/google/btree` 实现有序集合，使查找、删除和重新插入为 `O(log n)`。

### 10.4 largest-first split

如果不存在单笔 y 可以覆盖 x，则反复取反方向剩余金额最大的 Intent，分配 `min(x.remaining, y.remaining)`，直到 x 完整覆盖。

### 10.5 输出与不变量

内部 Allocation 可用于调试，但最终每个 Intent 只输出一个 IntentResult：

```text
FallbackAmount = OriginalAmount - MatchedAmount
```

每组必须满足：

```text
0 <= MatchedAmount <= OriginalAmount
MatchedAmount + FallbackAmount = OriginalAmount
sum matched(Lower→Higher) = sum matched(Higher→Lower)
```

每个方向最大净额化金额为两个方向原始总额的最小值。目标复杂度为时间 `O(n log n)`、空间 `O(n)`。

## 11. Merkle 承诺

### 11.1 哈希规则

新增 `pkg/netting/merkle`：

```text
LeafHash = SHA256(0x00 || Domain || CanonicalPayload)
NodeHash = SHA256(0x01 || LeftHash || RightHash)
```

集合先按协议规定排序。奇数叶子复制最后一个叶子。空树使用固定 EmptyRoot。Proof 明确记录兄弟节点方向。

### 11.2 三个根

```text
CutRoot              = MerkleRoot(ShardCut，按 ShardID)
IntentResultRoot     = MerkleRoot(IntentResult，按 IntentID)
ShardSettlementRoot  = MerkleRoot(ShardCommitment，按 ShardID)

MatchRoot = SHA256(
    "BLOCKEMULATOR_MATCH_ROOT_V1"
    || CutRoot
    || IntentResultRoot
    || ShardSettlementRoot
)

BatchID = SHA256(
    "BLOCKEMULATOR_BATCH_ID_V1"
    || PreviousBatchID
    || WindowID
    || MatchRoot
)
```

### 11.3 两级分片执行树

ShardSettlement 中携带的 BatchID 是在 MatchRoot 生成后附加的路由与防重放字段，不参与 ChunkRoot、ShardRoot 或 ShardSettlementRoot 的 CanonicalPayload，避免形成“BatchID 依赖 MatchRoot、MatchRoot 又依赖 BatchID”的循环。承诺中的 Chunk 内容绑定：

- WindowID、ShardID、ChunkIndex 和 ChunkCount。
- Outgoing、Incoming 的指令根。
- matched 和 fallback 金额汇总。

全局 ShardSettlementRoot 的叶子是每个分片的 ShardCommitment。每个 ShardCommitment 的 ShardRoot 再承诺该分片的 ChunkCommitment 列表。

分片执行 Chunk 时：

1. 从 Chunk 内容重算 ChunkRoot。
2. 验证 ChunkRoot 到 ShardRoot。
3. 验证 ShardCommitment 到 ShardSettlementRoot。
4. 结合 CutRoot 和 IntentResultRoot 重建 MatchRoot。

## 12. Beacon Chain

### 12.1 拓扑

```go
const BeaconShardID int64 = 0x7ffffffe
```

新增 `cmd/beaconnode`，配置四个 Beacon 节点。Beacon 复用现有 PrePrepare、Prepare、Commit 阶段，使用专用 BeaconShardOp 和 MatchRootBlockType。

### 12.2 链上内容

Beacon MatchRootBlock 持久化：

- BatchID。
- PreviousBatchID。
- WindowID。
- CutRoot。
- IntentResultRoot。
- ShardSettlementRoot。
- MatchRoot。
- 每个普通分片的 ShardCut。

Cuts 体积只随分片数增长。每笔 Intent 和 Settlement 记录不进入 Beacon 链上区块存储。

### 12.3 validation sidecar

Solver 提交：

```go
type BatchProposal struct {
    Header  MatchRootBlockBody
    Sidecar BatchSidecar
}

type BatchSidecar struct {
    FinalizedBlocks  []FinalizedBlockReceipt
    IntentResults    []IntentResult
    ShardSettlements []ShardSettlement
}
```

PrePrepare 传播 sidecar，Prepare 和 Commit 只传播 Proposal digest。Beacon 节点验证后将根写入 Beacon 链，并把 sidecar 写入独立 BatchStore。实验必须统计 sidecar 的传播字节和验证成本。

### 12.4 验证规则

Beacon 验证：

1. PreviousBatchID 和 WindowID 连续。
2. 每个 ShardCut 从上一 committed cut 连续推进。
3. Receipt 覆盖 Cut 中的连续区块，ParentHash 连续。
4. Receipt 来自正确普通分片 leader。
5. Intent ID 唯一且未被旧 Batch 消费。
6. Intent 在 Beacon 接受时未过期。
7. Cut 范围内每个 Intent 有且只有一个 IntentResult。
8. matched、fallback 和分片对双向金额守恒。
9. ShardSettlement 可以由 IntentResult 唯一推导。
10. 所有根、MatchRoot 和 BatchID 重算一致。

任何检查失败，Beacon follower 都不发送 Prepare。

## 13. ShardSettlement

```go
type ShardSettlement struct {
    BatchID    BatchID
    WindowID   uint64
    ShardID    int64
    ChunkIndex uint32
    ChunkCount uint32
    Outgoing   []IntentResult
    Incoming   []IntentResult
}
```

Outgoing 包含当前分片作为 SourceShard 的 Intent。Incoming 包含当前分片作为 DestinationShard 且 MatchedAmount 大于零的 Intent。

执行前验证：

- MatchRoot 已由 Beacon 最终确认。
- Chunk、ShardCommitment 和 MatchRoot proof 正确。
- Batch、ShardID、ChunkIndex 正确且未执行。
- 所有 Outgoing Reservation 为 Reserved，重新计算的 Intent ID、金额、nonce、源和目标一致。
- 每个 Intent 满足 matched + fallback = original。
- 按“对端分片—资产”满足 outgoing matched 等于 incoming matched。

先完成全部验证，再做任何状态修改。失败时整个 Chunk 不产生状态变化。

执行动作：

1. 将 Outgoing Reservation 记录为 Consumed，写入 BatchID、matched 和 fallback。
2. 从本分片 Escrow 向 Incoming 的原始 Recipient 支付 matched。
3. 对 Outgoing 的 fallback，从 Escrow 移出对应金额并写入 FallbackOutbox。
4. 将 `(BatchID, ShardID, ChunkIndex)` 标记为 executed。

## 14. Reserved fallback

资金预留已经完成源分片扣款，因此未匹配部分不能先退回 Sender 再执行 Relay1。

```go
type ReservedFallback struct {
    IntentID          intent.ID
    BatchID           BatchID
    Sender            account.Address
    Recipient         account.Address
    SourceShard       int64
    DestinationShard  int64
    Amount            *big.Int
}
```

源分片 leader 可重复发送 Pending outbox。目标分片将 ReservedFallback 放入 TxPool，经正常 PBFT 验证 MatchRoot 证明后给 Recipient 入账，并按 `(IntentID, BatchID)` 标记 executed。重复消息幂等忽略。

第一版保留 completed outbox 记录用于实验审计，不做垃圾回收。

## 15. 消息和异步顺序

新增消息：

- FinalizedBlockReceiptMsg：普通分片 leader → Solver。
- BatchProposalMsg：Solver → Beacon leader。
- MatchRootFinalizedMsg：Beacon leader → Solver、普通分片节点、Supervisor。
- SettlementPackageMsg：Solver 或持有 sidecar 的 Beacon 节点 → 普通分片 leader。
- FallbackTxMsg：源分片 leader → 目标分片 leader。
- FallbackCompletedMsg：目标分片 leader → 源分片 leader、Supervisor。

普通分片维护：

```go
confirmedRoots     map[BatchID]MatchRootBlockBody
pendingSettlements map[BatchID][]ShardSettlementPackage
```

Package 先到则缓存；Root 先到则等待 Package；两者齐备后才进入 TxPool。重复 Root、Package、Chunk 和 fallback 均按 ID 幂等处理。

## 16. 数据可用性

第一版使用完整副本：

- Solver 持久化全部 sidecar。
- 每个验证 BatchProposal 的 Beacon 节点持久化 sidecar。
- MatchRoot 确认后，Solver 或任一持有 sidecar 的 Beacon 节点都能重新生成 SettlementPackage。
- Beacon Catch-up 只恢复链上 roots；缺失 sidecar 时从其他 Beacon 节点请求。

## 17. 到期语义

第一版不实现用户主动取消或源分片自行退款：

- Intent 在 MaxWindowDuration 内被封存到一个窗口。
- 未匹配金额在同一个批次中立即标记为 fallback。
- Beacon 接受 Batch 时 Intent 必须未过期。
- Beacon 已接受的 Intent 即使清算晚于 ExpiryEpoch 到达也可以执行。
- 配置必须保证 ExpiryDuration 大于 MaxWindowDuration、预期 Beacon 确认上界和清算安全余量之和。
- 系统失效超过有效期视为第一版实验失败，不执行可能与 Beacon 竞态的自动退款。

## 18. 错误处理

协议错误使用可被 `errors.Is` 识别的 sentinel errors，并用 `%w` 增加上下文。主要类别：非法/重复/过期 Intent，余额不足，区块缺口或冲突，非法 Cut、撮合、守恒或 proof，未知 Batch、重复 Chunk 和非法 Settlement。

规则：

- 非法用户 Intent 只拒绝自身。
- 预期的重复消息静默幂等处理。
- Receipt 缺口只暂停单一分片 watermark。
- 同高度不同 BlockHash 视为冲突并停止消费该分片。
- 未确认 Root 的 Settlement 只缓存。
- Settlement 任一验证失败时不允许部分写状态。
- 缓存设置配置上限，拒绝无限未来高度消息。
- 正常流程不使用 panic。

## 19. 配置

第一版增加：

```yaml
netting:
  enabled: true
  batch_size: 5000
  max_window_duration_ms: 2000
  max_future_block_buffer: 128
  max_frozen_windows: 16
  settlement_chunk_size: 500
  native_asset_only: true

beacon:
  shard_id: 2147483646
  node_num: 4
  block_interval: 2000
  block_record_dir: "./exp/beacon_block_record/"
  sidecar_dir: "./exp/beacon_sidecar/"

solver:
  state_dir: "./exp/solver/"
```

具体默认数值可在实现和基准测试中调整，但字段语义固定。

## 20. 指标

### 窗口

- WindowID、CloseReason、IntentCount。
- 每分片区块数和 Cut。
- WatermarkSkew。
- WindowOpenDuration。
- FrozenWindowQueueLength。

### 撮合

- ExactMatchedIntentCount。
- BestFitMatchedIntentCount。
- SplitMatchedIntentCount 和 SplitAllocationCount。
- MatchedIntentRatio、MatchedValueRatio。
- FallbackIntentRatio、FallbackValueRatio。

### Beacon

- BatchProposalBytes、SidecarBytes。
- PrePrepare、Prepare、Commit bytes。
- MerkleBuildTime、BatchValidationTime。
- BeaconConsensusLatency、MatchRootStorageBytes。

### 状态与端到端

- ReservationLatency、CapitalLockDuration。
- SettlementLatency、FallbackLatency、EndToEndLatency。
- StateReadCount、StateWriteCount。
- ProofVerificationTime。

## 21. 测试

### 21.1 协议单元测试

- Intent 确定性编码和固定向量。
- Signature 不影响 Intent ID。
- Merkle proof 正常、篡改、空树和奇数叶子。
- 完全相等、Best-Fit、largest-first split。
- 两方向总额相等和单方向输入。
- 重复 Intent ID 和极大 big.Int。
- 同输入重复运行结果相同。
- 随机属性测试验证金额守恒。
- Receipt 乱序、重复、缺口和冲突。
- BatchSize 和 MaxWindowDuration。
- 慢分片 Cut 不推进且不阻塞其他分片。

### 21.2 状态测试

- Reserve 后 Sender 减少且 Escrow 增加。
- nonce、余额、重复和过期错误。
- 同发送方多个 Intent 顺序执行。
- Settlement 后 ReservationConsumed、Recipient 入账和 fallback outbox。
- 重复 Chunk 和 fallback 不重复入账。
- 失败 Chunk 完整回滚。
- 多分片原生资产总量守恒。

### 21.3 组件集成

- Receipt → Window → Matcher → BatchProposal → Beacon validation → SettlementPackage。
- Root/Package 两种到达顺序。
- Chunk 乱序、重复和恢复。
- FrozenWindow 和 Solver 重启恢复。

### 21.4 端到端

运行四个普通分片、每分片四个节点、四个 Beacon 节点、一个 Solver 和一个 Supervisor。要求所有合法 Intent 完成，Reservation 不残留，Batch/Cut 连续，无重复入账，全局资产守恒，原有 static relay/broker 行为不回归。

## 22. Git 协作和提交

代码阶段采用“修改和测试 → 展示未提交 diff → 用户批准 → commit”的交互方式，不主动 push。

计划提交：

```text
docs(netting): add protocol design
feat(intent): add payment intent model
feat(merkle): add batch proof tree
feat(matcher): add deterministic netting
feat(window): add vector cut windows
feat(registry): add escrow reservations
feat(receipt): publish finalized blocks
feat(solver): build netting batches
feat(beacon): commit match roots
feat(settlement): execute shard batches
feat(relay): add reserved fallback
feat(metrics): record netting results
test(netting): add end-to-end coverage
```

## 23. 安全假设和不变量

第一版假设网络参与者按照协议运行、普通分片 leader 和 Beacon leader 不伪造身份；异步消息可以延迟、乱序和重复，但不会被恶意篡改。Byzantine 扩展不属于本轮实现。

必须始终满足：

```text
每个 Intent 最多属于一个 Window 和一个 Batch。
每个 Reservation 最多从 Reserved 转为 Consumed 一次。
每个 Settlement Chunk 和 fallback 最多产生一次余额影响。
每个分片的 Cut 区间连续且不重叠。
每组双向 matched 金额相等。
每个 Intent 的 matched + fallback = original。
所有普通分片、Escrow 和在途 fallback 的原生资产总量守恒。
```

## 24. 完成标准

- 两个窗口条件按本设计工作，空闲时无空批次。
- 普通分片不需要同步高度，Vector-Cut 连续且不重叠。
- 撮合结果确定，复杂度满足 `O(n log n)` 目标。
- Reservation、Settlement 和 fallback 幂等且资金守恒。
- MatchRoot、ShardRoot 和 Chunk proof 可独立重算和验证。
- 独立四节点 Beacon PBFT 能确认批次。
- 四乘四普通分片加四 Beacon 节点实验完整结束。
- 原有测试、构建和主要 static relay/broker 行为保持通过。
- 每个实现阶段保留独立、可解释的 Git 修改记录。
