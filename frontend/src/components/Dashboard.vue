<script setup lang="ts">
import { computed } from 'vue';
import Icon from './Icon.vue';
import { money, type User, type Dashboard } from '../lib/api';
const props=defineProps<{user:User;data:Dashboard}>();
const emit=defineEmits<{create:[];billing:[]}>();
const metrics=[{key:'total',label:'申请总数',icon:'FileText',note:'累计服务记录'},{key:'active',label:'进行中的服务',icon:'Clock3',note:'正在排队或处理中'},{key:'succeeded',label:'服务已完成',icon:'CheckCheck',note:'处理结束，费用已结算'},{key:'attention',label:'需要协助',icon:'CircleHelp',note:'等待进一步核对'}];
const max=computed(()=>Math.max(1,...props.data.trend.map(t=>t.count)));
</script>
<template>
 <section class="service-terms-callout" aria-label="费用结算与申请说明"><Icon name="CircleHelp" :size="26"/><p>费用按服务办理结果结算，不以退款到账为依据。申请尚未开始时可撤回，开始后可在详情跟踪。</p></section>
 <div class="metric-grid"><section v-for="(metric,index) in metrics" :key="metric.key" class="metric-card"><div><span>{{metric.label}}</span><Icon :name="metric.icon" :size="18"/></div><strong>{{data.counts[metric.key]||0}}<small>笔</small></strong><p><span class="metric-index">0{{index+1}}</span>{{metric.note}}</p></section></div>
 <div class="overview-workbench">
  <section class="activity-card"><div class="chart-heading"><div><span class="eyebrow">ACTIVITY / 14 DAYS</span><h3>最近的申请节奏</h3></div><span class="chart-legend"><i/>每日提交量</span></div><div class="chart" role="img" aria-label="近十四天的每日申请数量"><div v-for="(day,index) in data.trend" :key="day.date" class="chart-column" tabindex="0" :aria-label="`${day.date}，${day.count} 笔`"><span class="chart-tooltip">{{day.date}} · {{day.count}} 笔</span><div class="chart-bar" :class="{current:index===data.trend.length-1}" :style="{height:`${Math.max(2,(day.count/max)*88)}%`}"/><small>{{index%2===0||index===data.trend.length-1?day.date.slice(5).replace('-','/'):''}}</small></div></div><div class="chart-footnote">按申请提交日期统计，记录以账户实际数据为准。</div></section>
  <section class="balance-card"><div class="balance-label"><span>可支配积分</span><Icon name="Wallet" :size="20"/></div><div class="balance-amount">{{money(user.balance-user.frozen)}}<small>分</small></div><div class="balance-detail"><span>服务预留</span><strong>{{money(user.frozen)}} 分</strong></div><div class="balance-detail"><span>每笔服务价格</span><strong>{{money(user.service_fee)}} 分</strong></div><button class="balance-link" @click="emit('billing')">查阅账单<Icon name="ArrowUpRight" :size="16"/></button><small class="balance-note">1 积分 = 1 R</small></section>
 </div>
 <section class="request-callout"><span class="callout-number">+</span><div><h2>准备好提交下一笔了吗？</h2><p>填写账号资料，或一次导入多个账号。</p></div><button class="button secondary" @click="emit('create')">开始填写<Icon name="ArrowUpRight"/></button></section>
</template>
<style scoped>
.service-terms-callout{display:flex;align-items:flex-start;gap:16px;padding:24px 26px;margin-bottom:24px;border:1px solid #ecc3a6;border-left:5px solid #b54e26;border-radius:6px;background:#fff2e5;color:#963b1b}
.service-terms-callout>svg{flex-shrink:0;margin-top:6px}
.service-terms-callout p{margin:0;font-size:clamp(19px,1.65vw,26px);font-weight:700;line-height:1.75;letter-spacing:.3px}
@media(max-width:640px){.service-terms-callout{padding:18px 16px;gap:10px}.service-terms-callout>svg{width:20px;height:20px}}
</style>
