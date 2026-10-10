//go:build linux || darwin

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/state"
	"github.com/mdp/qrterminal/v3"
	"rsc.io/qr"
)

func cmdLogin(configPath string, args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	qrPNG := fs.String("qr-png", "", "also write the QR code as a PNG to this path (for agents that send it as an image)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wx-bot login [--qr-png PATH] <binding>\n")
		fs.PrintDefaults()
	}
	// Accept flags before or after the binding name.
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	name := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
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
			if *qrPNG != "" {
				if err := writeQRPNG(*qrPNG, imgURL); err != nil {
					fmt.Fprintf(os.Stderr, "wx-bot login: write qr png: %v\n", err)
				} else {
					fmt.Fprintf(os.Stdout, "QR PNG: %s\n", *qrPNG)
				}
			}
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

// writeQRPNG renders content as a QR PNG, replacing path atomically.
func writeQRPNG(path, content string) error {
	code, err := qr.Encode(content, qr.M)
	if err != nil {
		return err
	}
	code.Scale = 8
	return state.WriteAtomic(path, code.PNG())
}
