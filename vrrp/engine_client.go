package vrrp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hnakamur/ltsvlog"
	"github.com/hnakamur/netutil"
)

// engine represents an interface to a vrrp engine.
type engine interface {
	HAState(haState) error
}

// VIPsHAConfig represents the high availability configuration for a node in a
// vrrp cluster.
type VIPsHAConfig struct {
	HAConfig
	VIPInterface *net.Interface
	VIPs         []*VIPsHAConfigVIP
}

type VIPsHAConfigVIP struct {
	IP    net.IP
	IPNet *net.IPNet

	cancel context.CancelFunc
}

// VIPsUpdateEngine implements the Engine interface for testing purposes.
type VIPsUpdateEngine struct {
	Config *VIPsHAConfig
}

// HAState does nothing.
func (e *VIPsUpdateEngine) HAState(state haState) error {
	c := e.Config
	for i, vipCfg := range c.VIPs {
		ltsvlog.Logger.Info().String("msg", "before updateHAStateForVIP").Int("i", i).Sprintf("vipCfg", "%+v", vipCfg).Log()
		err := e.updateHAStateForVIP(state, vipCfg)
		if err != nil {
			// 1つのVIPの追加・削除に失敗しても他のVIPの追加・削除は行いたいので
			// ログ出力はするがエラーでも抜けずにループを継続する。
			ltsvlog.Logger.Err(err)
		}
	}
	return nil
}

func (e *VIPsUpdateEngine) updateHAStateForVIP(state haState, vipCfg *VIPsHAConfigVIP) error {
	c := e.Config
	hasVIP, err := netutil.HasAddr(c.VIPInterface, vipCfg.IP)
	if err != nil {
		return ltsvlog.WrapErr(err, func(err error) error {
			return fmt.Errorf("failed to check interface has VIP, err=%v", err)
		}).String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).Stack("")
	}

	if state == HAMaster {
		if hasVIP {
			ltsvlog.Logger.Info().String("msg", "HAState called but already aquired VIP").Sprintf("state", "%v", state).
				String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).
				Stringer("mask", vipCfg.IPNet.Mask).Log()
		} else {
			err := netutil.AddAddr(c.VIPInterface, vipCfg.IP, vipCfg.IPNet, "")
			if err != nil {
				return ltsvlog.WrapErr(err, func(err error) error {
					return fmt.Errorf("failed to add IP address, err=%v", err)
				}).String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).
					Stringer("mask", vipCfg.IPNet.Mask).Stack("")
			}
		}

		if vipCfg.cancel == nil {
			var ctx context.Context
			ctx, vipCfg.cancel = context.WithCancel(context.TODO())
			ltsvlog.Logger.Info().String("msg", "before go sendGARPLoop").Stringer("vip", vipCfg.IP).Log()
			go sendGARPLoop(ctx, c.VIPInterface, vipCfg.IP)
		}
	} else {
		if hasVIP {
			err := netutil.DelAddr(c.VIPInterface, vipCfg.IP, vipCfg.IPNet)
			if err != nil {
				return ltsvlog.WrapErr(err, func(err error) error {
					return fmt.Errorf("failed to delete IP address, err=%v", err)
				}).String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).
					Stringer("mask", vipCfg.IPNet.Mask).Stack("")
			}
		} else {
			ltsvlog.Logger.Info().String("msg", "HAState called but already released VIP").Sprintf("state", "%v", state).
				String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).
				Stringer("mask", vipCfg.IPNet.Mask).Log()
			return nil
		}
		if vipCfg.cancel != nil {
			vipCfg.cancel()
		}
	}
	ltsvlog.Logger.Info().String("msg", "HAState updated").Sprintf("state", "%v", state).
		String("interface", c.VIPInterface.Name).Stringer("vip", vipCfg.IP).
		Stringer("mask", vipCfg.IPNet.Mask).Log()
	return nil
}

func sendGARPLoop(ctx context.Context, intf *net.Interface, vip net.IP) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			err := netutil.SendGARP(intf, vip)
			if err != nil {
				ltsvlog.Logger.Err(ltsvlog.WrapErr(err, func(err error) error {
					return fmt.Errorf("failed to send GARP, err=%v", err)
				}).Stringer("vip", vip).Stack(""))
			}
			ltsvlog.Logger.Info().String("msg", "sent GARP").Stringer("vip", vip).Log()
		case <-ctx.Done():
			ltsvlog.Logger.Info().String("msg", "exiting sendGARPLoop").Stringer("vip", vip).Log()
			return
		}
	}
}
