# Stage latency definitions

All plotted values are seconds per completed original transaction. Durations are
computed within each repeat and then repeats are averaged with equal weight.

- **Monoxide / Source:** transaction creation to Relay1 commit (or final commit for
  an intra-shard transaction).
- **Monoxide / Target:** Relay1 commit to final Relay2 commit; this includes relay
  transfer and target-shard consensus.
- **BrokerChain / Source:** transaction creation to Broker1 commit.
- **BrokerChain / Coordination:** Broker1 commit to Broker2 creation.
- **BrokerChain / Target:** Broker2 creation to Broker2/final commit.
- **CLPA / Source:** transaction creation to Relay1 commit (or final commit for
  an intra-shard transaction). CLPA reconfiguration time is not subtracted.
- **CLPA / Target:** Relay1 commit to final commit. CLPA reconfiguration time is
  not subtracted.
- **Netting / Source:** transaction creation to reservation commit. Intra-shard
  transactions use creation to final commit.
- **Netting / Matching:** window-open duration + matching time + Merkle-build
  time + beacon consensus latency.
- **Netting / Settlement:** for intents with `UsedFallback=false`, end-to-end
  latency minus Source minus Matching; zero for fallback intents.
- **Netting / Fallback:** for intents with `UsedFallback=true`, end-to-end
  latency minus Source minus Matching; zero for normally settled intents.

Settlement and Fallback are summed over their respective intents but divided by
the count of all completed original transactions. They are therefore additive
contributions to the overall per-transaction mean, not conditional means.

The companion `*_clpa_control.csv` preserves full per-round control-plane time
for diagnostics only. No CLPA time is displayed in the transaction-stage plot.
