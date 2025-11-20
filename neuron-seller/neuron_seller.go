package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	neuronsdk "github.com/NeuronInnovations/neuron-go-hedera-sdk"
	commonlib "github.com/NeuronInnovations/neuron-go-hedera-sdk/common-lib"
	hedera_helper "github.com/NeuronInnovations/neuron-go-hedera-sdk/hedera"
	"github.com/NeuronInnovations/neuron-go-hedera-sdk/types"
	"github.com/hashgraph/hedera-sdk-go/v2"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type neuronSellerConfig struct {
	Enabled        bool
	Protocol       protocol.ID
	Version        string
	StreamInterval time.Duration
	SampleKind     string
}

type neuronSeller struct {
	cfg neuronSellerConfig
}

type piMetrics struct {
	Ts         float64 `json:"ts"`
	Brightness float64 `json:"brightness"`
}

var (
	neuronCfg     neuronSellerConfig
	neuronCfgOnce sync.Once
	neuronCfgErr  error
)

func neuronStreamingEnabled() bool {
	cfg, err := getNeuronSellerConfig()
	if err != nil {
		log.Fatalf("neuron-seller: invalid configuration: %v", err)
	}
	return cfg.Enabled
}

func runNeuronSellerNode() error {
	cfg, err := getNeuronSellerConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		log.Println("neuron-seller: Neuron SDK disabled (NEURON_ENABLE not set)")
		return nil
	}

	seller := &neuronSeller{cfg: cfg.ensureDefaults()}

	log.Printf(
		"neuron-seller: starting Neuron SDK (version=%s protocol=%s interval=%s)",
		seller.cfg.Version,
		seller.cfg.Protocol,
		seller.cfg.StreamInterval,
	)

	noopBuyerCase := func(ctx context.Context, h host.Host, b *commonlib.NodeBuffers) {}
	noopBuyerTopic := func(msg hedera.TopicMessage) {}

	neuronsdk.LaunchSDK(
		seller.cfg.Version,
		seller.cfg.Protocol,
		nil,
		noopBuyerCase,
		noopBuyerTopic,
		seller.handleSellerStream,
		seller.handleSellerTopicMessage,
	)
	return nil
}

func getNeuronSellerConfig() (neuronSellerConfig, error) {
	neuronCfgOnce.Do(func() {
		neuronCfg, neuronCfgErr = loadNeuronSellerConfig()
	})
	return neuronCfg, neuronCfgErr
}

func loadNeuronSellerConfig() (neuronSellerConfig, error) {
	cfg := neuronSellerConfig{
		Enabled:        parseEnvBool("NEURON_ENABLE", false),
		Protocol:       protocol.ID(getEnvOrDefault("NEURON_PROTOCOL_ID", "/localsense/brightness/v1")),
		Version:        getEnvOrDefault("NEURON_VERSION", "0.1.0"),
		StreamInterval: time.Duration(parseEnvInt("NEURON_STREAM_INTERVAL_SECONDS", 5)) * time.Second,
		SampleKind:     getEnvOrDefault("NEURON_SAMPLE_KIND", "brightness_sample"),
	}
	return cfg.ensureDefaults(), nil
}

func (c neuronSellerConfig) ensureDefaults() neuronSellerConfig {
	if c.StreamInterval <= 0 {
		c.StreamInterval = 5 * time.Second
	}
	if c.Protocol == "" {
		c.Protocol = protocol.ID("/localsense/brightness/v1")
	}
	if c.Version == "" {
		c.Version = "0.1.0"
	}
	if c.SampleKind == "" {
		c.SampleKind = "brightness_sample"
	}
	return c
}

func (s *neuronSeller) handleSellerStream(ctx context.Context, p2pHost host.Host, buffers *commonlib.NodeBuffers) {
	ticker := time.NewTicker(s.cfg.StreamInterval)
	defer ticker.Stop()

	log.Printf("neuron-seller: stream loop running (tick=%s)", s.cfg.StreamInterval)

	for {
		select {
		case <-ctx.Done():
			log.Println("neuron-seller: context cancelled, stopping stream loop")
			return
		case tick := <-ticker.C:
			if len(buffers.GetBufferMap()) == 0 {
				continue
			}

			metrics, err := fetchPiMetrics()
			if err != nil {
				log.Printf("neuron-seller: unable to fetch Pi metrics: %v", err)
				continue
			}

			payload, tsEpoch, err := s.buildSamplePayload(tick, metrics)
			if err != nil {
				log.Printf("neuron-seller: unable to build payload: %v", err)
				continue
			}

			s.broadcastSample(p2pHost, buffers, payload, tsEpoch, metrics.Brightness)
		}
	}
}

func (s *neuronSeller) handleSellerTopicMessage(msg hedera.TopicMessage) {
	if len(msg.Contents) == 0 {
		return
	}

	messageType, ok := types.CheckMessageType(msg.Contents)
	if !ok {
		log.Printf("neuron-seller: received stdIn message (unclassified): %s", string(msg.Contents))
		return
	}
	log.Printf("neuron-seller: topic message type=%s consensus_ts=%s", messageType, msg.ConsensusTimestamp)
}

func (s *neuronSeller) broadcastSample(
	p2pHost host.Host,
	buffers *commonlib.NodeBuffers,
	payload []byte,
	tsEpoch int64,
	brightness float64,
) {
	line := append(payload, '\n')
	for peerID, bufferInfo := range buffers.GetBufferMap() {
		if bufferInfo.LibP2PState != types.Connected || !bufferInfo.IsOtherSideValidAccount {
			continue
		}

		if err := commonlib.WriteAndFlushBuffer(
			*bufferInfo,
			peerID,
			buffers,
			line,
			p2pHost,
			s.cfg.Protocol,
		); err != nil {
			log.Printf("neuron-seller: stream write to %s failed: %v", peerID, err)
			hedera_helper.PeerSendErrorMessage(
				bufferInfo.RequestOrResponse.OtherStdInTopic,
				types.WriteError,
				fmt.Sprintf("localsense node %s unavailable: %v", sellerCfg.SellerID, err),
				types.SendFreshHederaRequest,
			)
			continue
		}

		log.Printf(
			"neuron-seller: streamed brightness %.3f (ts=%d) to peer %s",
			brightness,
			tsEpoch,
			peerID,
		)
	}
}

func (s *neuronSeller) buildSamplePayload(now time.Time, metrics *piMetrics) ([]byte, int64, error) {
	if metrics == nil {
		return nil, 0, fmt.Errorf("metrics payload is nil")
	}

	tsEpoch := int64(metrics.Ts)
	var isoTime time.Time
	if tsEpoch > 0 {
		isoTime = time.Unix(tsEpoch, 0).UTC()
	} else {
		tsEpoch = now.UTC().Unix()
		isoTime = now.UTC()
	}

	payload := map[string]any{
		"ts":         tsEpoch,
		"ts_iso":     isoTime.Format(time.RFC3339),
		"brightness": metrics.Brightness,
		"seller_id":  sellerCfg.SellerID,
		"source":     sellerCfg.SellerID,
		"label":      sellerCfg.Label,
		"lat":        sellerCfg.Lat,
		"lon":        sellerCfg.Lon,
		"kind":       s.cfg.SampleKind,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal payload: %w", err)
	}
	return data, tsEpoch, nil
}

func fetchPiMetrics() (*piMetrics, error) {
	if sellerCfg.PiBase == "" {
		return nil, fmt.Errorf("PI_BASE_URL is not configured")
	}
	var metrics piMetrics
	if err := fetchJSON(sellerCfg.PiBase+"/metrics", &metrics); err != nil {
		return nil, err
	}
	return &metrics, nil
}

func getEnvOrDefault(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func parseEnvBool(key string, fallback bool) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if val == "" {
		return fallback
	}
	switch val {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func parseEnvInt(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("neuron-seller: invalid %s value %q, defaulting to %d", key, val, fallback)
		return fallback
	}
	return parsed
}
