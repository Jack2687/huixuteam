<script setup lang="ts">
import Icon from './Icon.vue';
const faqs=[
 ['账户里的积分代表什么？','每 1 积分对应 1 R。账户中显示的每笔服务价格就是成功办理所需的积分；例如 30 积分对应 30 R。'],
 ['怎样一次提交多个账号？','选择“提交申请”，切换到“批量填写”。可以上传 TXT、CSV，也可以粘贴表格三列。每批最多 500 个账号，按邮箱、密码、2FA 密钥的顺序填写。连续三条或更多横线可作分隔符；密码含首尾横线时请使用 CSV。'],
 ['预留积分什么时候扣除？','收件时按当时的服务价格预留积分，服务处理成功后结算。明确失败或排队时撤回会解冻；需要协助的申请暂时保留预留金额。同一申请成功只结算一次。'],
 ['等待安排时我能做什么？','申请会等待可用服务资源。你可以继续查看状态，也可以在服务开始前撤回。平台暂停服务或资源忙碌时，排队记录会保留。'],
 ['服务完成和资金到账是同一回事吗？','不是。服务完成表示本次申请的处理已成功，随后结算服务费。退款资金的金额和到账时间由上游决定，请另外核对收款情况。'],
 ['这里需要六位验证码吗？','需要的是验证器使用的 Base32 密钥或 otpauth 链接，不是短时间内变化的六位验证码。请在提交前检查资料是否正确。'],
 ['出现“需要协助”该怎么办？','请向服务人员提供申请编号以便核查，保留当前申请即可。重复提交同一账号不会加快处理。'],
 ['在哪里找到人工支持？','联系为你开通回序账户的服务人员。提供申请编号与问题描述即可，无需在聊天中再次发送密码和验证密钥。'],
];
</script>
<template><div class="help-layout"><aside class="guide-intro"><span class="eyebrow">START HERE</span><Icon name="CircleHelp" :size="40"/><h2>把疑问<br/>留在开始之前。</h2><p>了解资料格式、费用规则与状态含义，减少来回确认。</p><a href="/developers/reference" class="text-button" target="_blank" rel="noopener">需要接口接入？<Icon name="ArrowUpRight"/></a></aside><section class="help-card"><details v-for="([title,text],index) in faqs" :key="title"><summary><span class="faq-index">{{String(index+1).padStart(2,'0')}}</span>{{title}}<Icon name="Plus"/></summary><p>{{text}}</p></details></section></div></template>
