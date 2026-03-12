package cmd

import (
	"context"
	"fmt"

	"github.com/monody0007/tslink/internal/config"
	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Tailscale",
	Long: `Authenticate with your Tailscale account.
Opens a browser for OAuth login. Run this before 'tslink serve'.

Example:
  tslink login`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		stateDir, err := config.TsnetStateDir()
		if err != nil {
			return err
		}

		fmt.Println("→ Opening browser for Tailscale login...")

		srv := &tsnet.Server{
			Hostname: "tslink",
			Dir:      stateDir,
		}

		status, err := srv.Up(context.Background())
		if err != nil {
			srv.Close()
			return fmt.Errorf("login failed: %w", err)
		}

		loginName := ""
		lc, err := srv.LocalClient()
		if err == nil {
			st, err := lc.Status(context.Background())
			if err == nil && st.Self != nil {
				loginName = st.Self.UserID.String()
				if u, ok := st.User[st.Self.UserID]; ok {
					loginName = u.LoginName
				}
			}
		}
		_ = status

		srv.Close()

		if loginName != "" {
			fmt.Printf("→ ✓ Logged in: %s\n", loginName)
		} else {
			fmt.Println("→ ✓ Logged in to Tailscale")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
