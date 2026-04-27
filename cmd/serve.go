package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/miekg/dns"
	"github.com/spf13/cobra"

	hdns "github.com/scottzirkel/hostr/internal/dns"
)

var dnsAddr string

var serveDNSCmd = &cobra.Command{
	Use:    "serve-dns",
	Short:  "Run the *.test DNS responder (invoked by systemd)",
	Hidden: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		fmt.Printf("hostr-dns listening on %s\n", dnsAddr)
		return hdns.New(dnsAddr).Run(ctx)
	},
}

var queryCmd = &cobra.Command{
	Use:   "query <name>",
	Short: "Query hostr's DNS resolver directly (debug)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		c := new(dns.Client)
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(args[0]), dns.TypeA)
		r, _, err := c.Exchange(m, "127.0.0.1:1053")
		if err != nil {
			return err
		}
		fmt.Printf("rcode: %s\n", dns.RcodeToString[r.Rcode])
		for _, ans := range r.Answer {
			fmt.Println(ans)
		}
		if len(r.Answer) == 0 {
			fmt.Println("(no answer)")
		}
		return nil
	},
}

func init() {
	serveDNSCmd.Flags().StringVar(&dnsAddr, "addr", "127.0.0.1:1053", "address to listen on")
	rootCmd.AddCommand(serveDNSCmd, queryCmd)
}
