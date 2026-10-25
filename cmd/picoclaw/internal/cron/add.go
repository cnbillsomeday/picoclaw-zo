package cron

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func newAddCommand(storePath func() string) *cobra.Command {
	var (
		name    string
		message string
		every   int64
		cronExp string
		channel string
		to      string
		dice    bool
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new scheduled job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if every <= 0 && cronExp == "" {
				return fmt.Errorf("either --every or --cron must be specified")
			}

			var schedule cron.CronSchedule
			if every > 0 {
				everyMS := every * 1000
				schedule = cron.CronSchedule{Kind: "every", EveryMS: &everyMS}
			} else {
				schedule = cron.CronSchedule{Kind: "cron", Expr: cronExp}
			}

			cs := cron.NewCronService(storePath(), nil)
			job, err := cs.AddJob(name, schedule, message, channel, to)
			if err != nil {
				return fmt.Errorf("error adding job: %w", err)
			}

			if dice {
				job.Payload.Dice = &cron.CronDice{Enabled: true}
				if err := cs.UpdateJob(job); err != nil {
					return fmt.Errorf("error enabling dice for job: %w", err)
				}
			}

			fmt.Printf("✓ Added job '%s' (%s)\n", job.Name, job.ID)

			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Job name")
	cmd.Flags().StringVarP(&message, "message", "m", "", "Message for agent")
	cmd.Flags().Int64VarP(&every, "every", "e", 0, "Run every N seconds")
	cmd.Flags().StringVarP(&cronExp, "cron", "c", "", "Cron expression (e.g. '0 9 * * *')")
	cmd.Flags().StringVar(&to, "to", "", "Recipient for delivery")
	cmd.Flags().StringVar(&channel, "channel", "", "Channel for delivery")
	cmd.Flags().BoolVar(&dice, "dice", false, "先掷一次鱼骰子，没掷中就不跑 agent（主动搭话用）")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("message")
	cmd.MarkFlagsMutuallyExclusive("every", "cron")

	return cmd
}
