package main

import (
	"context"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func registerFeishuChatListeners(bus *events.Bus, svc *service.FeishuChatService) {
	if bus == nil || svc == nil {
		return
	}
	ctx := context.Background()
	bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		payload, ok := e.Payload.(protocol.ChatDonePayload)
		if !ok {
			return
		}
		if payload.ChatSessionID == "" || payload.Content == "" {
			return
		}
		if err := svc.SendAssistantReply(ctx, payload.ChatSessionID, payload.Content); err != nil {
			slog.Warn("feishu chat assistant reply send failed",
				"chat_session_id", payload.ChatSessionID,
				"message_id", payload.MessageID,
				"error", err)
		}
	})
}
