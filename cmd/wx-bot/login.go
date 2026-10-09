//go:build linux || darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/state"
	"github.com/mdp/qrterminal/v3"
)

func cmdLogin(configPath string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: wx-bot login <binding>\n")
		return 2
	}
	name := args[0]
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		return 1
	}
	if _, ok := cfg.Binding(name); !ok {
		fmt.Fprintf(os.Stderr, "wx-bot: unknown binding %q\n", name)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := ilink.NewLoginAPI()
	sess, err := api.Login(ctx, func(imgURL string, attempt int) {
		if attempt == 1 {
			fmt.Fprintln(os.Stdout, "请用微信扫描以下二维码登录：")
		} else {
			fmt.Fprintf(os.Stdout, "二维码已过期，请扫描新二维码（第 %d/%d 次）：\n", attempt, ilink.QRMaxExpiry)
		}
		if imgURL != "" {
			qrterminal.GenerateWithConfig(imgURL, qrterminal.Config{
				Level:          qrterminal.L,
				Writer:         os.Stdout,
				HalfBlocks:     true,
				BlackChar:      qrterminal.BLACK,
				WhiteChar:      qrterminal.WHITE,
				BlackWhiteChar: qrterminal.BLACK_WHITE,
				WhiteBlackChar: qrterminal.WHITE_BLACK,
				QuietZone:      1,
				WithSixel:      false,
			})
			fmt.Fprintln(os.Stdout, imgURL)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot login: %v\n", err)
		return 1
	}

	store, err := state.Open(cfg.StateDir, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot login: state: %v\n", err)
		return 1
	}
	if err := store.SaveSession(state.FromIlink(sess)); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot login: save session: %v\n", err)
		return 1
	}
	if err := store.ClearExpired(); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot login: clear expired: %v\n", err)
		return 1
	}

	b, _ := cfg.Binding(name)
	allow := sess.UserID
	if len(b.AllowWxids) > 0 {
		allow = strings.Join(b.AllowWxids, ", ")
	}
	fmt.Fprintf(os.Stdout, "login ok\nbinding: %s\nilink_user_id: %s\nallowlist: %s\n", name, sess.UserID, allow)
	return 0
}
