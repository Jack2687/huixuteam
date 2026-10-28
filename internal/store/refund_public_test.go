package store

import (
	"strings"
	"testing"
)

func TestHistorical401MessagesDoNotImplyIncorrectPassword(t *testing.T) {
	for _, tc := range []struct{ status, old, current string }{
		{"failed", "密码验证失败（401），请核对 ChatGPT 登录密码。冻结服务费已释放。", passwordFailedMessage},
		{"processing", "密码验证失败（401），请核对 ChatGPT 登录密码。申请正在结束处理中。", passwordEndingMessage},
		{"attention", "密码验证失败（401），请核对 ChatGPT 登录密码。申请结束处理需要跟进，请联系服务专员。", passwordAttentionMessage},
	} {
		r := Refund{Status: tc.status, Message: tc.old}
		sanitizeRefund(&r)
		if r.Message != tc.current || r.ErrorCode != "login_verification_failed" || r.HTTPStatus != 401 || strings.Contains(r.Message, "密码") {
			t.Fatal(r)
		}
		if got := publicNotification(tc.old); got != tc.current {
			t.Fatal(got)
		}
		if got := publicNotification("child@example.test：" + tc.old); got != "child@example.test："+tc.current {
			t.Fatal(got)
		}
	}
}

func TestNotificationAllowlistHidesPrivateAndLegacyMessages(t *testing.T) {
	for _, message := range []string{"child@example.test：退款服务已完成", "兑换成功，积分已存入可用余额。", "已受理 2 笔退款申请，等待处理。", "处理时间延长，正在继续"} {
		if got := publicNotification(message); got != message {
			t.Errorf("valid notice changed: %q", got)
		}
	}
	for _, message := range []string{"child@example.test：export_and_deactivate upstream reply", "已受理 private-token 笔退款申请，等待处理。", "登录子号，移出团队", "https://chatgpt.com/backend-api/accounts/export_and_deactivate", "Bearer private-at"} {
		if got := publicNotification(message); got != "申请状态已更新，请查看申请详情。" {
			t.Errorf("private notification passed through: %q", got)
		}
	}
}
