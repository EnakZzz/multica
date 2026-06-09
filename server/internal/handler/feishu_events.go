package handler

import (
	"context"
	"log/slog"
	"os"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func FeishuEventLongConnectionEnabled() bool {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	if appID == "" || appSecret == "" {
		return false
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("FEISHU_WS_ENABLED"))); raw != "" {
		return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("FEISHU_EVENT_MODE")))
	return mode == "" || mode == "auto" || mode == "ws" || mode == "long_connection"
}

func (h *Handler) StartFeishuEventLongConnection(ctx context.Context) func(context.Context) error {
	if h == nil || h.FeishuIssues == nil || !FeishuEventLongConnectionEnabled() {
		return nil
	}

	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	verificationToken := strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FEISHU_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = lark.FeishuBaseUrl
	}

	eventDispatcher := dispatcher.NewEventDispatcher(verificationToken, "")
	eventDispatcher.OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		result := h.ProcessFeishuCardAction(ctx, feishuCardActionEventMap(event))
		toast := nestedMap(result, "toast")
		if toast == nil {
			return nil, nil
		}
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{
				Type:    mapString(toast, "type"),
				Content: mapString(toast, "content"),
			},
		}, nil
	})
	eventDispatcher.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		eventMap := feishuMessageReceiveEventMap(event)
		if eventID := mapString(eventMap, "event_id"); eventID != "" && h.FeishuIssues != nil {
			reserved, err := h.FeishuIssues.ReserveEvent(ctx, eventID)
			if err != nil {
				slog.Warn("feishu long connection message reserve failed", "event_id", eventID, "error", err)
				return nil
			}
			if !reserved {
				slog.Info("feishu duplicate message event ignored", "event_id", eventID)
				return nil
			}
		}
		result := h.ProcessFeishuIncomingMessage(ctx, eventMap)
		slog.Info("feishu message event handled", "status", result["status"])
		return nil
	})

	wsClient := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithDomain(baseURL),
		larkws.WithOnReady(func() {
			slog.Info("Feishu event long connection ready")
		}),
		larkws.WithOnError(func(err error) {
			slog.Warn("Feishu event long connection error", "error", err)
		}),
		larkws.WithOnReconnecting(func() {
			slog.Warn("Feishu event long connection reconnecting")
		}),
		larkws.WithOnReconnected(func() {
			slog.Info("Feishu event long connection reconnected")
		}),
		larkws.WithOnDisconnected(func() {
			slog.Warn("Feishu event long connection disconnected")
		}),
	)

	go func() {
		slog.Info("Feishu event long connection enabled")
		if err := wsClient.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Feishu event long connection stopped", "error", err)
		}
	}()

	return func(stopCtx context.Context) error {
		wsClient.Close()
		return nil
	}
}

func feishuCardActionEventMap(event *callback.CardActionTriggerEvent) map[string]any {
	out := map[string]any{}
	if event == nil || event.Event == nil {
		return out
	}
	req := event.Event
	operator := map[string]any{}
	if req.Operator != nil {
		operator["open_id"] = req.Operator.OpenID
		if req.Operator.UserID != nil {
			operator["user_id"] = *req.Operator.UserID
		}
		if req.Operator.TenantKey != nil {
			operator["tenant_key"] = *req.Operator.TenantKey
		}
	}
	action := map[string]any{}
	if req.Action != nil {
		action["value"] = req.Action.Value
		action["tag"] = req.Action.Tag
		action["option"] = req.Action.Option
		action["timezone"] = req.Action.Timezone
		action["name"] = req.Action.Name
		action["form_value"] = req.Action.FormValue
		action["input_value"] = req.Action.InputValue
		action["options"] = req.Action.Options
		action["checked"] = req.Action.Checked
	}
	contextMap := map[string]any{}
	if req.Context != nil {
		contextMap["open_message_id"] = req.Context.OpenMessageID
		contextMap["open_chat_id"] = req.Context.OpenChatID
		contextMap["url"] = req.Context.URL
		contextMap["preview_token"] = req.Context.PreviewToken
	}
	out["operator"] = operator
	out["action"] = action
	out["context"] = contextMap
	out["token"] = req.Token
	out["host"] = req.Host
	out["delivery_type"] = req.DeliveryType
	return out
}

func feishuMessageReceiveEventMap(event *larkim.P2MessageReceiveV1) map[string]any {
	out := map[string]any{}
	if event == nil || event.Event == nil {
		return out
	}
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		out["event_id"] = event.EventV2Base.Header.EventID
	}
	ev := event.Event
	if ev.Sender != nil && ev.Sender.SenderId != nil {
		senderID := map[string]any{}
		if ev.Sender.SenderId.OpenId != nil {
			senderID["open_id"] = *ev.Sender.SenderId.OpenId
		}
		if ev.Sender.SenderId.UserId != nil {
			senderID["user_id"] = *ev.Sender.SenderId.UserId
		}
		if ev.Sender.SenderId.UnionId != nil {
			senderID["union_id"] = *ev.Sender.SenderId.UnionId
		}
		sender := map[string]any{"sender_id": senderID}
		if ev.Sender.SenderType != nil {
			sender["sender_type"] = *ev.Sender.SenderType
		}
		out["sender"] = sender
	}
	message := map[string]any{}
	if ev.Message != nil {
		if ev.Message.MessageId != nil {
			message["message_id"] = *ev.Message.MessageId
		}
		if ev.Message.RootId != nil {
			message["root_id"] = *ev.Message.RootId
		}
		if ev.Message.ParentId != nil {
			message["parent_id"] = *ev.Message.ParentId
		}
		if ev.Message.ChatId != nil {
			message["chat_id"] = *ev.Message.ChatId
		}
		if ev.Message.MessageType != nil {
			message["message_type"] = *ev.Message.MessageType
		}
		if ev.Message.Content != nil {
			message["content"] = *ev.Message.Content
		}
	}
	out["message"] = message
	return out
}
