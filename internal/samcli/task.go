// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import "github.com/spf13/cobra"

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Send a message to an agent and stream its reply",
		Long:  "Send messages to your running agents and stream their replies.",
	}

	cmd.AddCommand(newTaskSendCmd())
	cmd.AddCommand(newTaskRunCmd())
	cmd.AddCommand(newTaskRenderCmd())

	return cmd
}
