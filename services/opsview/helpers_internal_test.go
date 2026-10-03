// Copyright 2026 Candace Labs

package opsview

import (
	"time"

	"github.com/candacelabs/csf/pkg/widget"
)

func secondsOf(seconds int) time.Duration { return time.Duration(seconds) * time.Second }

func cardOf(key string, card SessionCard) widget.KeyedItem[SessionCard] {
	return widget.KeyedItem[SessionCard]{Key: key, State: card}
}

func newCards(items ...widget.KeyedItem[SessionCard]) (widget.KeyedState[SessionCard], error) {
	return widget.NewKeyedState(items)
}
