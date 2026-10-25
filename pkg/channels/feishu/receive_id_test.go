package feishu

import (
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFeishuReceiveIDType(t *testing.T) {
	cases := map[string]string{
		"ou_9e148ab3b293522bda5060b76e088d64": larkim.CreateMessageV1ReceiveIDTypeOpenId,
		"on_58790e584e9082e7841dbc3ae9698715": larkim.CreateMessageV1ReceiveIDTypeUnionId,
		"oc_1234567890":                       larkim.CreateMessageV1ReceiveIDTypeChatId,
		"1396228334":                          larkim.CreateMessageV1ReceiveIDTypeChatId,
		"":                                    larkim.CreateMessageV1ReceiveIDTypeChatId,
	}
	for id, want := range cases {
		if got := feishuReceiveIDType(id); got != want {
			t.Fatalf("feishuReceiveIDType(%q) = %q, want %q", id, got, want)
		}
	}
}
