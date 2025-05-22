package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

type LndExporter struct {
	sync.Mutex
	metrics map[string]*prometheus.Desc

	rpcAddr      string
	tlsCertPath  string
	macaroonPath string

	timeout time.Duration

	exportPeerMetrics    bool
	exportPaymentMetrics bool
}

func newGlobalMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(namespace+"_"+metricName, docString, labels, nil)
}

func NewLightningExporter(namespace string, rpcAddr string, tlsCertPath string, macaroonPath string, timeout time.Duration) *LndExporter {
	return &LndExporter{
		rpcAddr:      rpcAddr,
		tlsCertPath:  tlsCertPath,
		macaroonPath: macaroonPath,
		timeout:      timeout,

		metrics: map[string]*prometheus.Desc{
			"up": newGlobalMetric(namespace, "up", "up", []string{}),

			"forwarding_history_info": newGlobalMetric(namespace, "forwarding_history_info", "forwarding_history_info",
				[]string{
					"peer_alias_in",
					"peer_alias_out",
					"amount_in",
					"amount_out",
					"fee",
					"channel_id_in",
					"channel_i_out",
					"timestamp_ns",
				}),

			"network_capacity_satoshis_total": newGlobalMetric(namespace, "network_capacity_satoshis_total", "network_capacity_satoshis_total", []string{}),
			"network_channels_total":          newGlobalMetric(namespace, "network_channels_total", "network_channels_total", []string{}),
			"network_nodes_total":             newGlobalMetric(namespace, "network_nodes_total", "network_nodes_total", []string{}),

			"instance_info": newGlobalMetric(namespace, "instance_info", "instance_info", []string{"alias", "pubkey", "version"}),

			"wallet_balance_satoshis":          newGlobalMetric(namespace, "wallet_balance_satoshis", "The wallet balance.", []string{"status"}),
			"peers":                            newGlobalMetric(namespace, "peers", "Number of currently connected peers.", []string{}),
			"channels":                         newGlobalMetric(namespace, "channels", "Number of channels", []string{"status"}),
			"block_height":                     newGlobalMetric(namespace, "block_height", "The node’s current view of the height of the best block", []string{}),
			"synced_to_chain":                  newGlobalMetric(namespace, "synced_to_chain", "The node’s current view of the height of the best block", []string{}),
			"channels_limbo_balance_satoshis":  newGlobalMetric(namespace, "channels_limbo_balance_satoshis", "The balance in satoshis encumbered in pending channels", []string{}),
			"channels_pending":                 newGlobalMetric(namespace, "channels_pending", "The total pending channels", []string{"status", "forced"}),
			"channels_waiting_close":           newGlobalMetric(namespace, "channels_waiting_close", "Channels waiting for closing tx to confirm", []string{}),
			"channels_local_balance_satoshis":  newGlobalMetric(namespace, "channels_local_balance_satoshis", "Sum of all channel sendable balances", []string{}),
			"channels_remote_balance_satoshis": newGlobalMetric(namespace, "channels_remote_balance_satoshis", "Sum of all channel receivable balances", []string{}),
			"channel_local_balance_satoshis":   newGlobalMetric(namespace, "channel_local_balance_satoshis", "The channel local balance", []string{"active", "remote_pubkey", "chan_point", "chan_id", "private", "initator"}),
			"channel_remote_balance_satoshis":  newGlobalMetric(namespace, "channel_remote_balance_satoshis", "The channel remote balance", []string{"active", "remote_pubkey", "chan_point", "chan_id", "private", "initator"}),
			"channel_capacity_satoshis":        newGlobalMetric(namespace, "channel_capacity_satoshis", "The channel total capacity", []string{"active", "remote_pubkey", "chan_point", "chan_id", "private", "initator"}),
			"channel_commit_fee_satoshis":      newGlobalMetric(namespace, "channel_commit_fee_satoshis", "The channel commit fee", []string{"active", "remote_pubkey", "chan_point", "chan_id", "private", "initator"}),
			"channel_balance_percentage":       newGlobalMetric(namespace, "channel_balance_percentage", "The channel local balance", []string{"active", "remote_pubkey", "chan_point", "chan_id", "private", "initator"}),

			"peer_info":                      newGlobalMetric(namespace, "peer_info", "peer_info", []string{"addr", "remote_pubkey", "direction"}),
			"peer_info_received_bytes_total": newGlobalMetric(namespace, "peer_info_received_bytes_total", "peer_info_received_bytes_total", []string{"addr"}),
			"peer_info_sent_bytes_total":     newGlobalMetric(namespace, "peer_info_sent_bytes_total", "peer_info_sent_bytes_total", []string{"addr"}),
		},

		exportPeerMetrics:    true,
		exportPaymentMetrics: true,
	}
}

func (c *LndExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range c.metrics {
		ch <- m
	}
}

func boolToFloat(b bool) float64 {
	if !b {
		return 0.0
	} else {
		return 1.0
	}
}

func getGrpcClient(rpcAddr string, tlsCertPath string, macaroonPath string) (*grpc.ClientConn, error) {
	tlsCreds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
	if err != nil {
		log.Println("Cannot get node tls credentials", err)
		return nil, err
	}

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		log.Println("Cannot read macaroon file", err)
		return nil, err
	}

	mac := &macaroon.Macaroon{}
	if err = mac.UnmarshalBinary(macaroonBytes); err != nil {
		log.Println("Cannot unmarshal macaroon", err)
		return nil, err
	}

	macOpts, err := macaroons.NewMacaroonCredential(mac)
	if err != nil {
		return nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
		grpc.WithPerRPCCredentials(macOpts),
		grpc.WithDefaultCallOptions(maxMsgRecvSize),
	}

	log.Printf("dialing rpcAddr: %s", rpcAddr)
	conn, err := grpc.Dial(rpcAddr, opts...)
	if err != nil {
		log.Printf("grpc.Dial() err: %s", err)
		return nil, err
	}

	return conn, nil
}

func (c *LndExporter) Collect(ch chan<- prometheus.Metric) {
	c.Lock()
	defer c.Unlock()

	con, err := getGrpcClient(c.rpcAddr, c.tlsCertPath, c.macaroonPath)
	if err != nil {
		log.Printf("getGrpcClient() err: %s", err)
		ch <- prometheus.MustNewConstMetric(c.metrics["up"], prometheus.GaugeValue, 0)
		return
	}
	defer func() {
		log.Println("closing connection")
		con.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rpcClient := lnrpc.NewLightningClient(con)

	stats, err := rpcClient.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		log.Printf("rpcClient.GetInfo() err: %s", err)
		ch <- prometheus.MustNewConstMetric(c.metrics["up"], prometheus.GaugeValue, 0.0)
		return
	}

	ch <- prometheus.MustNewConstMetric(c.metrics["instance_info"],
		prometheus.GaugeValue, 1.0,
		stats.Alias,
		stats.IdentityPubkey,
		stats.Version,
	)
	ch <- prometheus.MustNewConstMetric(c.metrics["peers"],
		prometheus.GaugeValue, float64(stats.NumPeers))
	ch <- prometheus.MustNewConstMetric(c.metrics["channels"],
		prometheus.GaugeValue, float64(stats.NumActiveChannels), "active")
	ch <- prometheus.MustNewConstMetric(c.metrics["channels"],
		prometheus.GaugeValue, float64(stats.NumPendingChannels), "pending")
	ch <- prometheus.MustNewConstMetric(c.metrics["channels"],
		prometheus.GaugeValue, float64(stats.NumInactiveChannels), "inactive")
	ch <- prometheus.MustNewConstMetric(c.metrics["block_height"],
		prometheus.GaugeValue, float64(stats.BlockHeight))
	ch <- prometheus.MustNewConstMetric(c.metrics["synced_to_chain"],
		prometheus.GaugeValue, boolToFloat(stats.SyncedToChain))

	if walletStats, err := rpcClient.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{}); err == nil {
		ch <- prometheus.MustNewConstMetric(c.metrics["wallet_balance_satoshis"],
			prometheus.GaugeValue, float64(walletStats.UnconfirmedBalance), "unconfirmed")
		ch <- prometheus.MustNewConstMetric(c.metrics["wallet_balance_satoshis"],
			prometheus.GaugeValue, float64(walletStats.ConfirmedBalance), "confirmed")
	} else {
		log.Printf("rpcClient.WalletBalance err: %s", err)
	}

	if pendingChannelsStats, err := rpcClient.PendingChannels(ctx, &lnrpc.PendingChannelsRequest{}); err == nil {
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_limbo_balance_satoshis"],
			prometheus.GaugeValue, float64(pendingChannelsStats.TotalLimboBalance))
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_pending"],
			prometheus.GaugeValue, float64(len(pendingChannelsStats.PendingOpenChannels)), "opening", "false")
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_pending"],
			prometheus.GaugeValue, float64(len(pendingChannelsStats.PendingForceClosingChannels)), "closing", "true")
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_waiting_close"],
			prometheus.GaugeValue, float64(len(pendingChannelsStats.WaitingCloseChannels)))
	} else {
		log.Printf("rpcClient.PendingChannels err: %s", err)
	}

	if channelsBalanceStats, err := rpcClient.ChannelBalance(ctx, &lnrpc.ChannelBalanceRequest{}); err == nil {
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_local_balance_satoshis"],
			prometheus.GaugeValue, float64(channelsBalanceStats.LocalBalance.GetSat()))
		ch <- prometheus.MustNewConstMetric(c.metrics["channels_remote_balance_satoshis"],
			prometheus.GaugeValue, float64(channelsBalanceStats.RemoteBalance.GetSat()))
	} else {
		log.Printf("rpcClient.ChannelBalance err: %s", err)
	}

	// todo: fix this
	//
	if c.exportPaymentMetrics {
		fwdReq := &lnrpc.ForwardingHistoryRequest{}
		if fwdHistoryStats, err := rpcClient.ForwardingHistory(ctx, fwdReq); err == nil {
			for _, f := range fwdHistoryStats.GetForwardingEvents() {
				ch <- prometheus.MustNewConstMetric(c.metrics["forwarding_history_info"],
					prometheus.GaugeValue, float64(1.0),
					f.PeerAliasIn,
					f.PeerAliasOut,
					strconv.FormatUint(f.AmtIn, 10),
					strconv.FormatUint(f.AmtOut, 10),
					strconv.FormatUint(f.Fee, 10),
					strconv.FormatUint(f.ChanIdIn, 10),
					strconv.FormatUint(f.ChanIdOut, 10),
					strconv.FormatUint(f.TimestampNs, 10),
				)
			}
		} else {
			log.Printf("rpcClient.ForwardingHistory err: %s", err)
		}
	}

	if networkInfo, err := rpcClient.GetNetworkInfo(ctx, &lnrpc.NetworkInfoRequest{}); err == nil {
		ch <- prometheus.MustNewConstMetric(c.metrics["network_capacity_satoshis_total"],
			prometheus.GaugeValue, float64(networkInfo.TotalNetworkCapacity))
		ch <- prometheus.MustNewConstMetric(c.metrics["network_channels_total"],
			prometheus.GaugeValue, float64(networkInfo.NumChannels))
		ch <- prometheus.MustNewConstMetric(c.metrics["network_nodes_total"],
			prometheus.GaugeValue, float64(networkInfo.NumNodes))
	}

	if channelBalanceStats, err := rpcClient.ListChannels(ctx, &lnrpc.ListChannelsRequest{}); err == nil {
		for _, channel := range channelBalanceStats.Channels {
			lbls := []string{
				strconv.FormatBool(channel.Active),
				channel.RemotePubkey,
				channel.ChannelPoint,
				strconv.FormatUint(channel.ChanId, 10),
				strconv.FormatBool(channel.Private),
				strconv.FormatBool(channel.Initiator),
			}

			realCapacity := float64(channel.Capacity) - float64(channel.CommitFee)
			balancePercentage := float64(channel.LocalBalance) / realCapacity

			ch <- prometheus.MustNewConstMetric(c.metrics["channel_local_balance_satoshis"],
				prometheus.GaugeValue, float64(channel.LocalBalance), lbls...)
			ch <- prometheus.MustNewConstMetric(c.metrics["channel_remote_balance_satoshis"],
				prometheus.GaugeValue, float64(channel.RemoteBalance), lbls...)
			ch <- prometheus.MustNewConstMetric(c.metrics["channel_capacity_satoshis"],
				prometheus.GaugeValue, float64(channel.Capacity), lbls...)
			ch <- prometheus.MustNewConstMetric(c.metrics["channel_commit_fee_satoshis"],
				prometheus.GaugeValue, float64(channel.CommitFee), lbls...)
			ch <- prometheus.MustNewConstMetric(c.metrics["channel_balance_percentage"],
				prometheus.GaugeValue, float64(balancePercentage), lbls...)
		}
	} else {
		log.Printf("rpcClient.GetChannelBalanceStats err: %s", err)
	}

	if c.exportPeerMetrics {
		peers, err := rpcClient.ListPeers(ctx, &lnrpc.ListPeersRequest{})
		if err != nil {
			for _, peer := range peers.GetPeers() {
				dir := "outbound"
				if peer.Inbound {
					dir = "inbound"
				}
				ch <- prometheus.MustNewConstMetric(c.metrics["peer_info"],
					prometheus.GaugeValue, 1.0,
					peer.Address,
					peer.PubKey,
					dir)

				ch <- prometheus.MustNewConstMetric(c.metrics["peer_info_received_bytes_total"],
					prometheus.CounterValue, float64(peer.BytesRecv), peer.Address)
				ch <- prometheus.MustNewConstMetric(c.metrics["peer_info_sent_bytes_total"],
					prometheus.CounterValue, float64(peer.BytesSent), peer.Address)
			}
		} else {
			log.Printf("rpcClient.ListPeers err: %s", err)
		}
	}

	ch <- prometheus.MustNewConstMetric(c.metrics["up"], prometheus.GaugeValue, 1.0)
}
