package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTimelineProjectionHistoryRetriesAndUnknown(t *testing.T) {
	base := time.Date(2026, 10, 26, 5, 18, 0, 0, time.UTC)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * time.Second) }
	ev := func(n int, m string, private bool) timelineEvent { return timelineEvent{m, at(n), private} }
	events := []timelineEvent{
		ev(2, "开始处理", false), ev(3, "正在发送团队邀请。", true), ev(8, "团队邀请已确认，准备登录账号。", true),
		ev(9, "正在使用密码和 2FA 登录受邀团队。", true), ev(20, "正在使用密码和 2FA 登录受邀团队。", true),
		ev(40, "已确认团队会话，准备提交导出并停用个人空间。", true), ev(41, "正在提交导出并停用个人空间。", true),
		ev(60, "opaque-protocol-canary export_and_deactivate private-password", true), ev(60, "申请需要跟进，请联系服务专员。", false),
	}
	unknown := projectRefundTimeline("attention", base, at(65), nil, nil, events)
	var stopped RefundTimelineItem
	for _, r := range unknown.Items {
		if r.Stage == "停" {
			stopped = r
		}
	}
	if unknown.Active || stopped.Status != "待核实" || stopped.DurationSeconds == nil || *stopped.DurationSeconds != 19 {
		t.Fatal("unknown result not retained", stopped)
	}
	events = append(events, ev(70, "管理员已核实停用受理，将继续移出团队。", true), ev(70, "正在继续处理", false), ev(75, "正在移出本次账号。", true), ev(90, "退款服务已完成", false))
	accepted, completed := at(70), at(90)
	result := projectRefundTimeline("succeeded", base, at(999), &accepted, &completed, events)
	if result.Active {
		t.Fatal("terminal timeline still active")
	}
	var logins []RefundTimelineItem
	for _, r := range result.Items {
		if r.Stage == "登" {
			logins = append(logins, r)
		}
		if r.Stage == "移" && (r.DurationSeconds == nil || *r.DurationSeconds != 15) {
			t.Fatal("cleanup duration", r)
		}
	}
	if len(logins) != 2 || logins[0].Status != "已重试" || logins[1].Attempt != 2 || *logins[1].DurationSeconds != 20 {
		t.Fatal("retry history", logins)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "export_and_deactivate") {
		t.Fatal("private diagnostics leaked")
	}
	again, _ := json.Marshal(projectRefundTimeline("succeeded", base, at(1999), &accepted, &completed, events).Items)
	original, _ := json.Marshal(result.Items)
	if string(again) != string(original) {
		t.Fatal("completed times changed on refresh")
	}
}

func TestTimelineDoesNotInventMissingHistoryOrInterpretUnknownText(t *testing.T) {
	now := time.Now().UTC()
	created := now.Add(-time.Minute)
	events := []timelineEvent{{"退款服务已完成", now, false}, {"prefix 正在发送团队邀请。 secret", now, true}, {"正在使用密码和 2FA 登录受邀团队。", now, false}}
	result := projectRefundTimeline("succeeded", created, now, nil, nil, events)
	if result.Items[1].DurationSeconds != nil || result.Items[1].FinishedAt != nil {
		t.Fatal("missing queue end invented", result.Items[1])
	}
	for _, r := range result.Items {
		if r.Stage == "邀" || r.Stage == "登" || r.Stage == "停" {
			if r.Status != "未记录" || r.StartedAt != nil || r.FinishedAt != nil || r.DurationSeconds != nil {
				t.Fatal("invented historical time", r)
			}
		}
	}
	queued := projectRefundTimeline("queued", created, now, nil, nil, nil)
	if !queued.Active || queued.Items[1].Status != "排队中" || *queued.Items[1].DurationSeconds != 60 {
		t.Fatal("queue timing", queued)
	}
	cancelled := projectRefundTimeline("cancelled", created, now, nil, nil, []timelineEvent{{"申请已取消，冻结积分已释放。", now, false}})
	if cancelled.Active {
		t.Fatal("cancelled active")
	}
}
