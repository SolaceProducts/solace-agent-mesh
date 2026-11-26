// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type RetiredQueue struct {
	Queue  string
	Topics []string
}

func RetireQueue(b broker.Broker, r RetiredQueue, logger *slog.Logger) error {
	logger = logger.With("queue", r.Queue, "topics", r.Topics)

	q, done, err := bindExisting(b, r.Queue, "retire", logger)
	if done || err != nil {
		return err
	}
	unsubscribed, err := removeSubscriptions(q, r.Queue, r.Topics)
	if err != nil {
		return err
	}
	q.Close()
	deleted, err := deleteQueue(b, r.Queue, "retire", unsubscribed, logger)
	if deleted {
		logger.Info("queue retired")
	}
	return err
}

const (
	msgBindUnsupported = "queue cleanup skipped: broker cannot bind an existing queue"
	msgBindRefused     = "queue cleanup refused: cannot bind the queue, so it keeps collecting messages; delete it on the broker"
	msgDeleteRefused   = "queue cleanup refused: cannot delete the queue; it no longer receives messages; delete it on the broker"
	msgCleanupRefused  = "queue cleanup refused: cannot delete the queue or remove its subscriptions, so it keeps collecting messages; delete it on the broker"
)

func bindExisting(b broker.Broker, name, action string, logger *slog.Logger) (q broker.BoundQueue, done bool, err error) {
	binder, ok := b.(broker.ExistingQueueBinder)
	if !ok {
		logger.Warn(msgBindUnsupported, "action", action)
		return nil, true, nil
	}
	q, err = binder.BindExisting(name)
	switch {
	case errors.Is(err, broker.ErrQueueNotFound):
		return nil, true, nil
	case errors.Is(err, errors.ErrUnsupported):
		logger.Warn(msgBindUnsupported, "action", action)
		return nil, true, nil
	case errors.Is(err, broker.ErrPermissionDenied):
		logger.Error(msgBindRefused, "action", action, "error", err)
		return nil, true, nil
	case err != nil:
		return nil, false, fmt.Errorf("bind queue %q: %w", name, err)
	}
	return q, false, nil
}

func removeSubscriptions(q broker.BoundQueue, name string, topics []string) (unsubscribed bool, err error) {
	unsubscribed = true
	for _, topic := range topics {
		removeErr := q.RemoveSubscription(topic)
		if errors.Is(removeErr, broker.ErrPermissionDenied) {
			unsubscribed = false
			continue
		}
		if removeErr != nil {
			q.Close()
			return false, fmt.Errorf("remove subscription %q from queue %q: %w", topic, name, removeErr)
		}
	}
	return unsubscribed, nil
}

func deleteQueue(b broker.Broker, name, action string, unsubscribed bool, logger *slog.Logger) (deleted bool, err error) {
	err = b.DeprovisionQueue(name)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, broker.ErrPermissionDenied):
		return false, fmt.Errorf("delete queue %q: %w", name, err)
	case unsubscribed:
		logger.Warn(msgDeleteRefused, "action", action, "error", err)
	default:
		logger.Error(msgCleanupRefused, "action", action, "error", err)
	}
	return false, nil
}
