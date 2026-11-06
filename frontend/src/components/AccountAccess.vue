<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import Icon from './Icon.vue';
import LoginSlider from './LoginSlider.vue';
const slider=ref<InstanceType<typeof LoginSlider>|null>(null),sliderProof=ref(''),invite=ref(new URLSearchParams(location.search).get('invite')||'');
import { api, type User } from '../lib/api';
import type { PublicSite } from '../lib/site';
const props=defineProps<{site:PublicSite;sessionError:string}>();
const emit=defineEmits<{login:[user:User]}>();
const mode=ref<'login'|'register'|'reset'>('login');
const email=ref(''),password=ref(''),confirmation=ref(''),name=ref(''),code=ref('');
const busy=ref(false),sending=ref(false),error=ref(''),message=ref(''),seconds=ref(0);
const title=computed(()=>({login:'登录回序',register:'创建服务账户',reset:'重置账户密码'})[mode.value]);
const needsCode=computed(()=>mode.value==='reset'||mode.value==='register'&&props.site.email_verification_enabled);
let timer:ReturnType<typeof setInterval>|undefined;
function switchMode(value:typeof mode.value){mode.value=value;password.value='';confirmation.value='';code.value='';error.value='';message.value='';}
watch(()=>props.site,()=>{if(mode.value==='register'&&!props.site.registration_enabled||mode.value==='reset'&&!props.site.password_reset_enabled)switchMode('login')});
async function sendCode(){
 if(!email.value||busy.value||sending.value||seconds.value)return;
 sending.value=true;error.value='';
 try{
  const result=await api<{message:string;retry_after:number}>('/api/auth/email-code',{method:'POST',body:JSON.stringify({email:email.value,purpose:mode.value==='reset'?'reset':'register'})});
  message.value=result.message;seconds.value=result.retry_after;
  clearInterval(timer);timer=setInterval(()=>{seconds.value--;if(seconds.value<=0)clearInterval(timer)},1000);
 }catch(e){error.value=(e as Error).message}finally{sending.value=false}
}
async function submit(){
 if(busy.value||sending.value)return;error.value='';message.value='';
 const bytes=(value:string)=>new TextEncoder().encode(value).length;
 if(mode.value!=='login'&&(bytes(password.value)<12||bytes(password.value)>72)){error.value='密码须为 12～72 字节，中文和表情占多个字节';return}
 if(mode.value==='register'&&(bytes(name.value.trim())<1||bytes(name.value.trim())>80)){error.value='显示名称须为 1～80 字节';return}
 if(mode.value!=='login'&&password.value!==confirmation.value){error.value='两次输入的密码不一致';return}
 busy.value=true;
 try{
  if(mode.value==='login'){
   const user=await api<User>('/api/auth/login',{method:'POST',body:JSON.stringify({email:email.value,password:password.value,slider_proof:sliderProof.value})});password.value='';emit('login',user);
  }else{
   const register=mode.value==='register';
   await api(register?'/api/auth/register':'/api/auth/reset-password',{method:'POST',body:JSON.stringify({email:email.value,password:password.value,code:code.value,...(register?{name:name.value,invite:invite.value}:{})})});
   switchMode('login');message.value=register?'注册成功，请使用新账号登录。':'密码已重置，旧登录和 API 密钥已失效，请重新登录。';
  }
 }catch(e){error.value=(e as Error).message;if(mode.value==='login')void slider.value?.reset()}finally{busy.value=false}
}
onUnmounted(()=>clearInterval(timer));
</script>
<template>
 <div class="account-access"><span class="eyebrow">ACCOUNT ACCESS</span><h2>{{title}}</h2><p>{{mode==='login'?'使用你的账号进入服务台。':mode==='register'?'完成账户资料，即可开始使用回序。':'通过账户邮箱验证码设置新的登录密码。'}}</p>
  <form @submit.prevent="submit">
   <label v-if="mode==='register'">显示名称<input v-model="name" autocomplete="nickname" maxlength="80" required placeholder="如何称呼你"/></label>
   <label>账户邮箱<input v-model="email" type="email" autocomplete="username" maxlength="254" :disabled="busy||sending" required placeholder="name@example.com"/></label>
   <p v-if="mode==='register'&&site.email_domains.length" class="field-help">可注册邮箱：{{site.email_domains.join('、')}}</p>
   <label v-if="needsCode">邮箱验证码<div class="code-row"><input v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required placeholder="6 位验证码"/><button type="button" class="button secondary" :disabled="busy||sending||seconds>0||!email" @click="sendCode">{{sending?'发送中…':seconds>0?`${seconds} 秒后重发`:'获取验证码'}}</button></div></label>
   <label>{{mode==='login'?'账户密码':'设置密码'}}<input v-model="password" type="password" :autocomplete="mode==='login'?'current-password':'new-password'" :minlength="1" maxlength="72" required :placeholder="mode==='login'?'输入登录密码':'12～72 字节，建议混合字母和数字'"/></label>
   <label v-if="mode!=='login'">确认密码<input v-model="confirmation" type="password" autocomplete="new-password" minlength="1" maxlength="72" required placeholder="再次输入密码"/></label>
   <p v-if="error||(mode==='login'&&sessionError)" class="form-error" role="alert">{{error||sessionError}}</p><p v-if="message" class="auth-message" role="status">{{message}}</p>
   <label v-if="mode==='register'">邀请标识（选填）<input v-model="invite" maxlength="80" placeholder="来自伙伴的邀请标识"/></label>
   <LoginSlider v-if="mode==='login'&&site.login_slider_enabled!==false" ref="slider" @verified="sliderProof=$event"/>
   <button class="button primary full" :disabled="busy||sending"><span>{{busy?'正在处理…':mode==='login'?'进入服务台':mode==='register'?'创建账户':'确认重置密码'}}</span><Icon :name="busy?'LoaderCircle':'ArrowUpRight'" :class="{spin:busy}"/></button>
  </form>
  <div class="auth-links"><template v-if="mode==='login'"><button v-if="site.registration_enabled" type="button" :disabled="busy||sending" @click="switchMode('register')">注册新账户</button><span v-else>没有账户？请联系服务人员。</span><button v-if="site.password_reset_enabled" type="button" :disabled="busy||sending" @click="switchMode('reset')">忘记密码？</button></template><button v-else type="button" :disabled="busy||sending" @click="switchMode('login')">返回登录</button></div>
 </div>
</template>
<style scoped>
.code-row{display:flex;gap:10px}.code-row input{min-width:0;flex:1}.code-row .button{white-space:nowrap;font-size:12px;padding:10px}.auth-links{display:flex;justify-content:space-between;gap:12px;font-size:12px;margin-top:24px;color:var(--accent)}.auth-links span{color:var(--muted)}.auth-message{font-size:12px;color:#3c6548;line-height:1.7;background:#eaf0e4;padding:12px;border-radius:6px}.field-help{font-size:11px;color:var(--muted);overflow-wrap:anywhere;margin-top:-10px}.account-access>p{font-size:12px;color:var(--muted)}
</style>
