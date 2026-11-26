// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"
	"net"
)

type recoveringConn struct {
	net.Conn
	logger *slog.Logger
}

func (c *recoveringConn) Write(p []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n = 0
			err = fmt.Errorf("recovered panic during conn write: %v", r)
			if c.logger != nil {
				c.logger.LogAttrs(context.Background(), slog.LevelError,
					"recovered conn write panic",
					slog.String("eventType", "email.imap_conn_write_panic_recovered"),
					slog.String("error", fmt.Sprintf("%v", r)),
				)
			}
		}
	}()
	return c.Conn.Write(p)
}

func (c *recoveringConn) Read(p []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n = 0
			err = fmt.Errorf("recovered panic during conn read: %v", r)
			if c.logger != nil {
				c.logger.LogAttrs(context.Background(), slog.LevelError,
					"recovered conn read panic",
					slog.String("eventType", "email.imap_conn_read_panic_recovered"),
					slog.String("error", fmt.Sprintf("%v", r)),
				)
			}
		}
	}()
	return c.Conn.Read(p)
}
