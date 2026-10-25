<script setup lang="ts">
import Brand from './Brand.vue';
import Icon from './Icon.vue';
const base=location.origin+'/open/v1/requests';
const endpoints=[
 {id:'submit',method:'POST',path:base,title:'创建服务申请',description:'提交 1～500 个账号。每次提交需携带 Idempotency-Key（8～128 字符）。成功受理返回 HTTP 202，以及 ids、replayed、provider_ready 字段。'},
 {id:'list',method:'GET',path:base,title:'检索申请记录',description:'仅返回此密钥所属账户的记录。可传 page、size（1～100）、q、status；响应为 items、total、page、size。'},
 {id:'detail',method:'GET',path:base+'/{id}',title:'读取服务进度',description:'使用提交返回的申请 ID。响应中 refund 为申请信息，events 为服务动态，provider_ready 为服务可用状态。查询不返回密码或 2FA 密钥。'},
 {id:'withdraw',method:'POST',path:base+'/{id}/withdraw',title:'撤回未开始的申请',description:'仅 queued 状态允许撤回。撤回后释放本笔预留积分；重复撤回不会多次返还。请求不需要 JSON 正文。'},
];
const payload=JSON.stringify({authorized:true,accounts:[{email:'account@example.com',password:'ACCOUNT_PASSWORD',totp_secret:'TOTP_SECRET'}]},null,2);
</script>
<template>
 <div class="api-docs"><header class="docs-topbar"><Brand/><a class="button secondary" href="/#developer">回到服务台<Icon name="ArrowUpRight"/></a></header>
 <div class="docs-layout"><aside class="docs-nav"><span class="eyebrow">OPEN API / V1</span><h3>接口索引</h3><a href="#authentication">认证与约定</a><a v-for="e in endpoints" :key="e.id" :href="'#'+e.id">{{e.title}}</a><a href="#states">状态与错误</a></aside>
 <main class="docs-main"><div class="docs-hero"><span class="eyebrow">HUIXU / DEVELOPER REFERENCE</span><h1>回序开放接口</h1><p>从你的服务端提交申请，并持续读取处理进展。</p><code>{{base}}</code></div>
 <section id="authentication" class="doc-endpoint"><h2>连接前的约定</h2><div class="docs-facts"><div><small>身份验证</small><code>Authorization: Bearer API_KEY</code></div><div><small>提交格式</small><code>Content-Type: application/json</code></div><div><small>防止重复提交</small><code>Idempotency-Key: unique-request-id</code></div></div><p>访问密钥在「接口接入」页面生成，请保存在服务端。金额字段是百分之一积分的整数：3000 对应 30 积分，即 30 R。</p></section>
 <section v-for="e in endpoints" :id="e.id" :key="e.id" class="doc-endpoint"><div class="endpoint-title"><span class="method-tag" :class="e.method.toLowerCase()">{{e.method}}</span><h2>{{e.title}}</h2></div><code class="endpoint-url">{{e.path}}</code><p>{{e.description}}</p><template v-if="e.id==='submit'"><pre class="request-payload">{{payload}}</pre><p>同一账户使用同一个幂等键与相同内容重试，会得到原申请；内容不一致返回 409。批量申请共同校验、共同入账，校验不通过时不接受其中任何一笔。</p></template></section>
 <section id="states" class="doc-endpoint"><h2>读懂处理结果</h2><div class="notice">202 表示已收件，只有 succeeded 表示服务处理完毕。服务完成后结算费用，不以退款资金到账作为完成条件。</div><div class="docs-state-grid"><div v-for="[code,label] in [['queued','等待安排'],['processing','正在办理'],['verifying','结果核对'],['attention','需要协助'],['succeeded','服务完成'],['failed','办理未成功'],['cancelled','已撤回'],['banned','账号受限']]" :key="code"><code>{{code}}</code><span>{{label}}</span></div></div><p>建议每 15 秒查询一次；成功、失败或撤回后结束轮询。attention 需要人工跟进，请保留申请编号，不要重新提交。</p><h3>请求异常</h3><p>400 参数不合法 · 401 密钥无效 · 402 可用积分不足 · 403 无访问权限 · 404 记录或路径不存在 · 409 状态/幂等冲突 · 415 内容类型不符 · 500 服务暂时异常。</p></section>
 <footer class="page-footer">回序 HUIXU · 开放接口手册</footer></main></div></div>
</template>
