# 预实验一：真实以太坊 workload 中的双向抵消空间

这个文件夹是一个**自包含复现包**，用于和 coauthor 共享预实验一的原始数据、数据生成脚本、画图脚本和当前图片。

coauthor 不需要完整 BlockEmulator-X 仓库，也可以直接运行本文件夹中的 Python 脚本，重新生成实验 CSV 和论文图。

## 文件结构

```text
预实验一/
├── README.md
├── run_full_experiment.sh
├── Raw_data/
│   └── 25250000区块高度-25255233区块高度_500K.csv
├── 画图_500K_inject_1w/
│   ├── height_matching_rate.csv
│   ├── directional_pair_ctx_counts.csv
│   ├── global_height_matching_rate.png
│   ├── directional_ctx_count_heatmap.png
│   └── bidirectional_netting_potential_heatmap.png
└── 生成画图数据/
    ├── generate_exp1_fifo_data.py
    ├── generate_exp2_sensitivity_data.py
    ├── plot_exp1_height_matching_rate.py
    ├── plot_directional_ctx_heatmap.py
    ├── plot_bidirectional_netting_potential_heatmap.py
    ├── plot_exp2_sensitivity_bars.py
    ├── exp1_real_workload_netting.go
    ├── run_exp2_sensitivity.sh
    └── run_exp1_real_workload_netting.sh
```

其中：

- `Raw_data/`：已提取好的前 500K 笔有效以太坊交易 CSV。
- `生成画图数据/generate_exp1_fifo_data.py`：自包含 Python 数据生成脚本，不依赖完整 Go 仓库。
- `生成画图数据/generate_exp2_sensitivity_data.py`：批量运行分片数量/注入速度敏感性实验。
- `生成画图数据/plot_exp1_height_matching_rate.py`：画全网逐区块撮合率柱状图。
- `生成画图数据/plot_directional_ctx_heatmap.py`：画各分片之间有向 CTX 数量热力图。
- `生成画图数据/plot_bidirectional_netting_potential_heatmap.py`：画各无向分片对之间的双向抵消潜力热力图。
- `生成画图数据/plot_exp2_sensitivity_bars.py`：画敏感性实验柱状图。
- `生成画图数据/run_exp2_sensitivity.sh`：一键生成敏感性实验数据和图片。
- `生成画图数据/exp1_real_workload_netting.go`：早期 Go 版本/参考实现，复现时不必使用。
- `画图_500K_inject_1w/height_matching_rate.csv`：已经生成好的主图数据。
- `画图_500K_inject_1w/directional_pair_ctx_counts.csv`：已经生成好的热力图数据。
- `画图_500K_inject_1w/global_height_matching_rate.png`：逐区块撮合率柱状图。
- `画图_500K_inject_1w/directional_ctx_count_heatmap.png`：分片间 CTX 数量热力图。
- `画图_500K_inject_1w/bidirectional_netting_potential_heatmap.png`：分片对双向抵消潜力热力图。
- `run_full_experiment.sh`：一键重新生成数据并画图。

## 实验在做什么

实验问题：

```text
真实以太坊 workload 中是否存在跨分片交易的双向抵消空间？
```

实验流程：

1. 从 `Raw_data/25250000区块高度-25255233区块高度_500K.csv` 读取以太坊交易。
2. 过滤无效交易：
   - `from` 或 `to` 地址无法解析；
   - `to` 缺失或为 `None`；
   - `from == to`；
   - `value <= 0`。
3. 使用已经提取好的前 `500,000` 笔有效交易。
4. 用 hash-style account-to-shard mapping 将账户映射到 `8` 个分片。
5. 每轮注入 `10,000` 笔有效交易。
6. 每个分片每个区块高度最多打包 `2,000` 笔交易。
7. 对跨分片正金额交易，即 CTX，执行 FIFO split netting。
8. 对每个区块高度统计撮合率。

## 实验配置

```text
有效交易数量: 500,000
分片数量: 8
账户划分: hash-style account-to-shard mapping
每轮注入: 10,000 笔有效交易
每片区块大小: 2,000 笔交易
撮合算法: FIFO split
纵轴指标: fully matched CTX / total CTX
```

账户划分方法与原 BlockEmulator-X 中的 `partition.DefaultAccountLoc` 等价：

```text
shard_id = big_endian_uint32(last_4_bytes(address)) mod shard_count
```

## FIFO split 撮合算法

FIFO split 是本实验用于展示双向抵消空间的较弱撮合算法。

算法含义：

```text
按照交易在 workload 中出现的原始顺序处理。
每个分片对维护两个方向队列，例如 S1->S2 和 S2->S1。
新来的交易只和反方向最早尚未完成的交易撮合。
允许 split。
如果反方向最早交易被完全消耗，再继续撮合下一笔反方向交易。
如果新来的交易还有剩余，则排入本方向队尾。
```

撮合率定义：

```text
matching_rate = fully_matched_ctx / total_ctx
```

其中：

- `total_ctx`：某个区块高度中的全部跨分片正金额交易数量；
- `fully_matched_ctx`：撮合后 `matched_amount == original_amount` 的 CTX 数量。

## 快速重新画图

如果只想基于已有 CSV 重新润色逐区块撮合率柱状图，运行：

```bash
cd /path/to/预实验一
python3 生成画图数据/plot_exp1_height_matching_rate.py \
  --input 画图_500K_inject_1w/height_matching_rate.csv \
  --output 画图_500K_inject_1w/global_height_matching_rate.png
```

如果只想基于已有 CSV 重新画分片间 CTX 数量热力图，运行：

```bash
cd /path/to/预实验一
python3 生成画图数据/plot_directional_ctx_heatmap.py \
  --input 画图_500K_inject_1w/directional_pair_ctx_counts.csv \
  --output 画图_500K_inject_1w/directional_ctx_count_heatmap.png
```

如果想画更适合展示“双向交易数量接近程度”的抵消潜力热力图，运行：

```bash
cd /path/to/预实验一
python3 生成画图数据/plot_bidirectional_netting_potential_heatmap.py \
  --input 画图_500K_inject_1w/bidirectional_pair_ctx_balance.csv \
  --output 画图_500K_inject_1w/bidirectional_netting_potential_heatmap.png \
  --csv-output 画图_500K_inject_1w/bidirectional_netting_potential.csv
```

该图中每个无向分片对 `(Si, Sj)` 的颜色为：

```text
2 * min(CTX_i_to_j, CTX_j_to_i) / (CTX_i_to_j + CTX_j_to_i)
```

颜色越深，表示两个方向的 CTX 数量越接近，数量层面的双向抵消空间越强。格子中的第二行标注为两个方向的 CTX 数量 `a/b`。

画图依赖：

```bash
python3 -m pip install pillow
```

当前图的风格：

- Times New Roman 字体；
- 论文风格白底；
- 柱状图；
- 横坐标：`Block height`；
- 纵坐标：`Matching rate`；
- 纵轴截断为 `50%–100%`；
- 图内标注整体撮合率和均值线；
- 输出 PNG。

当前图标题：

```text
Block-wise Cross-Shard Transaction Matching Rate under FIFO Netting
```

## 完整复现实验

从原始 CSV 重新生成数据和图片，运行：

```bash
cd /path/to/预实验一
bash run_full_experiment.sh
```

这条命令会依次执行：

1. `生成画图数据/generate_exp1_fifo_data.py`
2. `生成画图数据/plot_exp1_height_matching_rate.py`
3. `生成画图数据/plot_directional_ctx_heatmap.py`

生成/覆盖以下文件：

```text
画图_500K_inject_1w/height_matching_rate.csv
画图_500K_inject_1w/pair_height_matching_rate.csv
画图_500K_inject_1w/pair_aggregate_matching_rate.csv
画图_500K_inject_1w/directional_pair_ctx_counts.csv
画图_500K_inject_1w/bidirectional_pair_ctx_balance.csv
画图_500K_inject_1w/bidirectional_netting_potential.csv
画图_500K_inject_1w/run_summary.csv
画图_500K_inject_1w/global_height_matching_rate.png
画图_500K_inject_1w/directional_ctx_count_heatmap.png
画图_500K_inject_1w/bidirectional_netting_potential_heatmap.png
```

如果要切换注入速度，例如每轮注入 8K 笔交易：

```bash
cd /path/to/预实验一
INJECT_SIZE=8000 OUTPUT_DIR="$PWD/画图_500K_inject_8k" bash run_full_experiment.sh
```

如果只想重新生成 CSV，不画图：

```bash
cd /path/to/预实验一
python3 生成画图数据/generate_exp1_fifo_data.py \
  --input Raw_data/25250000区块高度-25255233区块高度_500K.csv \
  --output-dir 画图_500K_inject_1w \
  --shards 8 \
  --inject-size 10000 \
  --block-size 2000 \
  --max-valid-txs 500000
```

## 输出文件说明

```text
height_matching_rate.csv
```

每个区块高度的全网撮合率，用于主图。

```text
pair_height_matching_rate.csv
```

每个区块高度、每个无向分片对的撮合率。

```text
pair_aggregate_matching_rate.csv
```

每个无向分片对在全部窗口上的聚合撮合率。

```text
directional_pair_ctx_counts.csv
```

有方向的 CTX 数量，例如 `S0->S1` 和 `S1->S0` 分开统计。
该文件也是 `directional_ctx_count_heatmap.png` 的输入数据。

```text
bidirectional_pair_ctx_balance.csv
```

每个无向分片对两个方向的数量和值分布对比。

```text
bidirectional_netting_potential.csv
```

每个无向分片对的 `2*min(a,b)/(a+b)` 双向抵消潜力比例，以及 `2*min(a,b)` 可双向覆盖的 CTX 数量。

```text
run_summary.csv
```

实验参数、过滤计数和总体撮合率。

## 给 coauthor 的建议

如果只是润色图片，重点看：

```text
画图_500K_inject_1w/height_matching_rate.csv
画图_500K_inject_1w/directional_pair_ctx_counts.csv
画图_500K_inject_1w/bidirectional_pair_ctx_balance.csv
画图_500K_inject_1w/bidirectional_netting_potential.csv
生成画图数据/plot_exp1_height_matching_rate.py
生成画图数据/plot_directional_ctx_heatmap.py
生成画图数据/plot_bidirectional_netting_potential_heatmap.py
画图_500K_inject_1w/global_height_matching_rate.png
画图_500K_inject_1w/directional_ctx_count_heatmap.png
画图_500K_inject_1w/bidirectional_netting_potential_heatmap.png
```

如果要完整复现实验，直接运行：

```bash
bash run_full_experiment.sh
```

这个文件夹已经包含 raw CSV、数据生成脚本和画图脚本，不需要完整 BlockEmulator-X 仓库。

## 额外敏感性实验

本文件夹还包含两个基于同一份 500K 有效交易数据的敏感性实验。

实验 A：不同分片数量下的平均交易撮合率。

```text
分片数量: 4, 8, 16, 32
每轮注入: 5,000, 10,000, 20,000, 40,000 笔有效交易
注入速度设计: 分片数翻倍时，注入速度也相应提高，以保证高分片场景交易供给充足
每片区块大小: 2,000 笔交易
撮合算法: FIFO split
纵轴指标: 全局撮合率 fully_matched_ctx / total_ctx
```

实验 B：8 分片下，不同交易注入速度的平均交易撮合率。

```text
分片数量: 8
每轮注入: 8,000, 10,000, 12,000, 14,000 笔有效交易
每片区块大小: 2,000 笔交易
撮合算法: FIFO split
纵轴指标: 全局撮合率 fully_matched_ctx / total_ctx
```

一键运行：

```bash
cd /path/to/预实验一
bash 生成画图数据/run_exp2_sensitivity.sh
```

这条命令会生成：

```text
画图_sensitivity/shard_count_sweep.csv
画图_sensitivity/inject_size_sweep.csv
画图_sensitivity/shard_count_vs_matching_rate.png
画图_sensitivity/inject_speed_vs_matching_rate.png
```

其中：

- `shard_count_sweep.csv`：记录 4、8、16、32 分片下的全局撮合率、平均撮合率和 CTX 数量。
- `inject_size_sweep.csv`：记录 8k、10k、12k、14k 注入速度下的全局撮合率、平均撮合率和 CTX 数量。
- `shard_count_vs_matching_rate.png`：横坐标为分片数量，纵坐标为全局交易撮合率。
- `inject_speed_vs_matching_rate.png`：横坐标为交易注入速度，纵坐标为全局交易撮合率。

如果只想重新画图、不重新跑数据：

```bash
cd /path/to/预实验一
python3 生成画图数据/plot_exp2_sensitivity_bars.py \
  --shard-input 画图_sensitivity/shard_count_sweep.csv \
  --inject-input 画图_sensitivity/inject_size_sweep.csv \
  --output-dir 画图_sensitivity \
  --metric global_matching_rate
```

如果想临时查看“按区块高度撮合率再算术平均”的口径，画图时改用：

```bash
python3 生成画图数据/plot_exp2_sensitivity_bars.py \
  --metric avg_height_matching_rate \
  --shard-input 画图_sensitivity/shard_count_sweep.csv \
  --inject-input 画图_sensitivity/inject_size_sweep.csv \
  --output-dir 画图_sensitivity
```
