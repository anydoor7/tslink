package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/output"
	qrcode "github.com/skip2/go-qrcode"
)

// Payloads use proven runtime URLs; an enabled pending portal never falls back
// to an app address. Invitation QR requires both explicit link flags.
func preparePeopleQR(r *PeopleResult, args peopleArguments) error {
	payload := ""
	if r.Portal.Enabled {
		payload = r.Portal.URL
	} else {
		for _, g := range r.Person.Grants {
			if g.Active && g.URL != "" {
				payload = g.URL
				break
			}
		}
	}
	r.Guide = []string{"Install Tailscale on your phone.", "Sign in with the account the owner invited.", "Open any invitation the owner sent and accept it.", "Keep Tailscale connected. Open the home address and bookmark it."}
	r.GuideZH = []string{"在手机上安装 Tailscale。", "用主人邀请的账号登录。", "打开主人发来的邀请并接受。", "保持 Tailscale 已连接，打开入口地址并收藏。"}
	if payload != "" {
		r.Guide[3] = "Keep Tailscale connected. Open " + payload + " and bookmark it."
		r.GuideZH[3] = "保持 Tailscale 已连接，打开 " + payload + " 并收藏。"
	}
	if !args.QR {
		return nil
	}
	if args.QRInvite != "" {
		if !args.PrintLinks {
			return output.ErrUsage("--qr-invite requires --print-links; an invitation QR is a credential")
		}
		payload = ""
		for _, inv := range r.Invites {
			if inv.App == args.QRInvite {
				payload = inv.InviteURL
				break
			}
		}
		r.QRWarning = "This invitation QR is a credential. Send it only to the intended person."
	}
	u, err := url.Parse(payload)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(payload) > 2048 {
		return output.ErrUsage("QR address is not ready. Access is saved; retry people update --qr after the owner checks status --urls")
	}
	r.QRPayload = payload
	r.QRTerminal = true
	return nil
}

func qrPNG(payload, path string) error {
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return err
	}
	b, err := qr.PNG(-8)
	if err != nil {
		return err
	}
	return atomicfile.WriteFileInExistingDir(path, b, 0o600)
}

// Two vertical modules per cell, without ANSI codes. The four-module quiet
// zone comes from Bitmap. A light terminal background is needed for scanning.
func terminalQR(payload string) (string, error) {
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return "", err
	}
	matrix := qr.Bitmap()
	var out strings.Builder
	for y := 0; y < len(matrix); y += 2 {
		for x := range matrix[y] {
			top, bottom := matrix[y][x], false
			if y+1 < len(matrix) {
				bottom = matrix[y+1][x]
			}
			switch {
			case top && bottom:
				out.WriteRune('█')
			case top:
				out.WriteRune('▀')
			case bottom:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func formatPhoneGuide(steps []string) string {
	var out strings.Builder
	for i, step := range steps {
		fmt.Fprintf(&out, "%d. %s\n", i+1, step)
	}
	return out.String()
}
