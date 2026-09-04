package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/cmd/loadnetwork"
	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/logger"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/solver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

const solverWaitingTime = 5 * time.Second

var configPath = flag.String("config", "config.yaml", "path to config file")

func main() {
	flag.Parse()
	lp, err := config.LoadLocalParams()
	if err != nil {
		log.Fatal(fmt.Errorf("load local parameters: %w", err))
	}
	if lp.ShardID != nodetopo.SolverShardID || lp.NodeID != 0 {
		log.Fatal(fmt.Errorf("solver must run as shard %d node 0", nodetopo.SolverShardID))
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
	store, err := batchstore.Open(cfg.BatchStorePath)
	if err != nil {
		log.Fatal(fmt.Errorf("open batch store: %w", err))
	}
	defer func() { _ = store.Close() }()
	node, err := solver.New(solver.Config{
		ShardCount:        cfg.ShardNum,
		BatchSize:         cfg.BatchSize,
		MaxWindowDuration: time.Duration(cfg.MaxWindowDurationMS) * time.Millisecond,
		TickInterval:      time.Duration(cfg.SolverTickIntervalMS) * time.Millisecond,
		MetricsEnabled:    cfg.MetricsEnabled,
	}, network.NewConnHandler(p2p), resolver, store)
	if err != nil {
		log.Fatal(fmt.Errorf("create solver: %w", err))
	}
	time.Sleep(solverWaitingTime)
	if err = node.Start(context.Background()); err != nil {
		log.Fatal(fmt.Errorf("run solver: %w", err))
	}
}
