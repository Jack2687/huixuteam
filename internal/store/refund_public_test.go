package store

import "testing"

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
