package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/cmd/loadnetwork"
	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/consensus/pbft"
	"github.com/HuangLab-SYSU/block-emulator-x/consensus/pbft/insideop/beaconop"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/logger"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

const beaconWaitingTime = 5 * time.Second

var configPath = flag.String("config", "config.yaml", "path to config file")

func main() {
	flag.Parse()
	lp, err := config.LoadLocalParams()
	if err != nil {
		log.Fatal(fmt.Errorf("load local parameters: %w", err))
	}
	if lp.ShardID != nodetopo.BeaconShardID {
		log.Fatal(fmt.Errorf("Beacon node must use shard %d", nodetopo.BeaconShardID))
	}
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(fmt.Errorf("load config: %w", err))
	}
	if err = logger.InitLogger(lp, cfg.LogCfg); err != nil {
		log.Fatal(fmt.Errorf("init logger: %w", err))
	}
	defer logger.CloseLoggerFile()
	p2p, resolver, err := loadnetwork.PrepareNetworkByCfg(cfg, lp)
	if err != nil {
		log.Fatal(fmt.Errorf("prepare network: %w", err))
	}
	storePath := fmt.Sprintf("%s.node-%d", cfg.BeaconStorePath, lp.NodeID)
	store, err := beacon.OpenStore(storePath)
	if err != nil {
		log.Fatal(fmt.Errorf("open Beacon store: %w", err))
	}
	defer func() { _ = store.Close() }()
	conn := network.NewConnHandler(p2p)
	op := beaconop.New(conn, resolver, store, cfg.ShardNum, lp.NodeID)
	node, err := pbft.NewSpecialPBFTNode(conn, resolver, cfg.ConsensusNodeCfg, *lp, op, op)
	if err != nil {
		log.Fatal(fmt.Errorf("create Beacon PBFT node: %w", err))
	}
	time.Sleep(beaconWaitingTime)
	node.Start()
}
