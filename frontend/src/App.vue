<script setup lang="ts">
import DataProtectionNotice from "./components/DataProtectionNotice.vue";
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from "vue";
import Brand from "./components/Brand.vue";
import AccountAccess from "./components/AccountAccess.vue";
import type { PublicSite, SiteMenu } from "./lib/site";
import { applyBranding, branding } from "./lib/branding";
const Announcements=defineAsyncComponent(()=>import("./components/Announcements.vue"));
import Icon from "./components/Icon.vue";
const DashboardView=defineAsyncComponent(()=>import("./components/Dashboard.vue"));
const RefundTable=defineAsyncComponent(()=>import("./components/RefundTable.vue"));
const RefundForm=defineAsyncComponent(()=>import("./components/RefundForm.vue"));
const RefundDetail=defineAsyncComponent(()=>import("./components/RefundDetail.vue"));
const Billing=defineAsyncComponent(()=>import("./components/Billing.vue"));
const Developers=defineAsyncComponent(()=>import("./components/Developers.vue"));
const Help=defineAsyncComponent(()=>import("./components/Help.vue"));
const ApiDocs=defineAsyncComponent(()=>import("./components/ApiDocs.vue"));
const Admin=defineAsyncComponent(()=>import("./components/Admin.vue"));
const CustomMenuPage=defineAsyncComponent(()=>import("./components/CustomMenuPage.vue"));
import {
  api,
  date,
  type User,
  type Dashboard,
  type Notification,
} from "./lib/api";
type BuiltinPage = 'overview' | 'refunds' | 'billing' | 'developer' | 'announcements' | 'help' | 'api-docs' | 'admin' | 'unavailable';
type PageName = BuiltinPage | `custom:${string}`;
const pages: Record<BuiltinPage,{label:string;eyebrow:string;title:string;subtitle:string;icon:string}> = {
 overview:{label:'总览',eyebrow:'01 / OVERVIEW',title:'服务总览',subtitle:'掌握申请进展，安排下一步工作。',icon:'LayoutDashboard'},
 refunds:{label:'申请记录',eyebrow:'02 / REQUESTS',title:'申请记录',subtitle:'从受理到完成，集中查阅每一笔服务记录。',icon:'FileText'},
 billing:{label:'资金与账单',eyebrow:'03 / BALANCE',title:'资金与账单',subtitle:'可用、预留与支出，每一笔变动均可追溯。',icon:'Wallet'},
 developer:{label:'接口接入',eyebrow:'04 / DEVELOPERS',title:'连接你的业务',subtitle:'管理访问凭证，让申请与进度融入你的系统。',icon:'CodeXml'},
 announcements:{label:'公告中心',eyebrow:'05 / BULLETIN',title:'公告中心',subtitle:'关注服务消息与产品更新。',icon:'Bell'},
 help:{label:'使用指南',eyebrow:'GUIDE / SUPPORT',title:'需要一点指引？',subtitle:'先了解申请规则，再开始使用回序。',icon:'CircleHelp'},
 'api-docs':{label:'接口手册',eyebrow:'',title:'',subtitle:'',icon:'CodeXml'},
 admin:{label:'',eyebrow:'',title:'',subtitle:'',icon:'ShieldCheck'},
 unavailable:{label:'服务台',eyebrow:'WORKSPACE',title:'暂无可用功能',subtitle:'如需帮助，请联系服务人员。',icon:'CircleHelp'},
};
function route(): PageName {
  const customPath=location.pathname.match(/^\/workspace\/custom\/([a-zA-Z0-9_-]+)$/);
  if(!location.hash&&customPath)return `custom:${customPath[1]}`;
  const value = location.hash.slice(1) || (location.pathname.startsWith("/workspace/") ? location.pathname.split("/")[2] : "");
  if(/^custom:[a-zA-Z0-9_-]+$/.test(value))return value as PageName;
  return (
    value in pages
      ? value
      : location.pathname === "/developers/reference"
        ? "api-docs"
        : "overview"
  ) as PageName;
}
const page = ref<PageName>(route()),
  meta = computed(() => page.value.startsWith('custom:')?{label:menus.value.find(item=>item.key===page.value)?.label||'自定义页面',eyebrow:'WORKSPACE / LINKS',title:menus.value.find(item=>item.key===page.value)?.label||'自定义页面',subtitle:'',icon:'ExternalLink'}:pages[page.value as BuiltinPage]);
const site=ref<PublicSite>({branding:branding.value,menus:[],registration_enabled:false,email_verification_enabled:true,email_domains:[],password_reset_enabled:false});
const menus=ref<SiteMenu[]>([]);
const nav=computed(()=>menus.value.filter(item=>item.key!=='help'&&item.key!=='admin'));
const adminMenu=computed(()=>user.value?.role==='admin'&&user.value.permissions.includes('admin.overview.read')?menus.value.find(item=>item.key==='admin'):undefined);
const allowed=(key:string)=>key==='admin'?!!adminMenu.value:key==='unavailable'||(key==='api-docs'?site.value.menus:menus.value).some(item=>item.key===(key==='api-docs'?'developer':key));
function ensurePage(){
 if(!allowed(page.value)){page.value=(menus.value.find(item=>!item.open_in_new_tab)?.key||'unavailable') as PageName;location.hash=page.value}
 if(!allowed('refunds')){creating.value=false;detailID.value='';search.value='';query.value=''}
}
async function loadSite(){site.value=await api<PublicSite>('/api/public/site');applyBranding(site.value.branding)}
const user = ref<User>(),
  booting = ref(true),
  loginError = ref(""),
  sidebar = ref(false),
  showNotifications = ref(false),
  creating = ref(false),
  detailID = ref(""),
  search = ref(""),
  query = ref(""),
  searchEl = ref<HTMLInputElement>();
const dashboard = ref<Dashboard>({
    counts: {},
    trend: [],
    provider_ready: false,
  }),
  notifications = ref<Notification[]>([]),
  refreshTick = ref(0),
  refreshing = ref(false),
  loadError = ref(""),
  toast = ref("");
let toastTimer: ReturnType<typeof setTimeout>,
  searchTimer: ReturnType<typeof setTimeout>,
  interval: ReturnType<typeof setInterval>;
let refreshPromise: Promise<void> | null = null;
let authGeneration = 0;
const initial = computed(() => user.value?.name[0]?.toUpperCase() || "A");
const unread = computed(
  () => notifications.value.filter((n) => !n.read_at).length,
);
function notify(message: string) {
  toast.value = message;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 4500);
}
function go(target: PageName) {
  if(!allowed(target))return;
  if (location.pathname === "/developers/reference") {
    location.href = `/#${target}`;
    return;
  }
  location.hash = target;
  page.value = target;
  sidebar.value = false;
  showNotifications.value = false;
  window.scrollTo({ top: 0 });
}
function hashChange() {
  page.value = route();
  if(!booting.value)ensurePage();
  sidebar.value = false;
  showNotifications.value = false;
}
function expire() {
  authGeneration++;
  user.value = undefined;
  creating.value = false;
  detailID.value = "";
  notifications.value = [];
  menus.value=site.value.menus;
  pages.admin.label='';pages.admin.title='';
  dashboard.value = { counts: {}, trend: [], provider_ready: false };
  loginError.value = "会话已结束，请再次登录。";
}
async function refresh() {
  if (!user.value || refreshPromise) return refreshPromise;
  const generation = authGeneration;
  refreshing.value = true;
  refreshPromise = (async () => {
    try {
      const [u, navigation] = await Promise.all([api<User>("/api/auth/me"),api<{menus:SiteMenu[]}>("/api/workspace/navigation"),loadSite()]);
      if(generation!==authGeneration)return;
      user.value=u;menus.value=navigation.menus;
      pages.admin.label=adminMenu.value?.label||'';pages.admin.title=adminMenu.value?.label||'';
      ensurePage();
      const [d,n]=await Promise.all([
        allowed('overview')?api<Dashboard>('/api/dashboard'):Promise.resolve({counts:{},trend:[],provider_ready:false}),
        api<{items:Notification[]}>('/api/notifications')
      ]);
      if(generation!==authGeneration)return;
      dashboard.value=d;notifications.value=n.items;
      loadError.value = "";
      refreshTick.value++;
    } catch (e) {
      if (generation === authGeneration) loadError.value = (e as Error).message;
    } finally {
      refreshing.value = false;
      refreshPromise = null;
    }
  })();
  return refreshPromise;
}
async function loggedIn(value:User){user.value=value;authGeneration++;loginError.value='';await refresh()}
async function logout() {
  try {
    await api("/api/auth/logout", { method: "POST" });
    authGeneration++;
    user.value = undefined;
    notifications.value = [];
    creating.value = false;
    detailID.value = "";
    search.value = "";
    loginError.value = "";
    menus.value=site.value.menus;page.value="overview";location.hash="overview";
    pages.admin.label='';pages.admin.title='';
  } catch (e) {
    notify((e as Error).message);
  }
}
async function created(ids: string[]) {
  creating.value = false;
  notify(`已保存 ${ids.length} 笔申请，已加入处理队列。`);
  await refresh();
  go("refunds");
}
async function markRead() {
  try {
    await api("/api/notifications/read", { method: "POST" });
    await refresh();
  } catch (e) {
    notify((e as Error).message);
  }
}
function keyboard(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    searchEl.value?.focus();
  }
  if (event.key === "Escape") {
    sidebar.value = false;
    showNotifications.value = false;
  }
}
watch(search, (value) => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    query.value = value;
    if (value && allowed("refunds") && page.value !== "refunds") go("refunds");
  }, 250);
});
onMounted(async () => {
  window.addEventListener("hashchange", hashChange);
  window.addEventListener("keydown", keyboard);
  window.addEventListener("session-expired", expire);
  try {
    await loadSite();menus.value=site.value.menus;
    user.value = await api<User>("/api/auth/me");
    await refresh();
  } catch {
    /* An unauthenticated visitor sees the login form. */
  } finally {
    booting.value = false;
  }
  interval = setInterval(() => {
    if (!document.hidden && user.value) refresh();
  }, 15000);
});
onUnmounted(() => {
  clearInterval(interval);
  clearTimeout(toastTimer);
  clearTimeout(searchTimer);
  window.removeEventListener("hashchange", hashChange);
  window.removeEventListener("keydown", keyboard);
  window.removeEventListener("session-expired", expire);
});
</script>
<template>
 <div v-if="booting" class="boot-screen"><Brand/><div class="loading"><Icon name="LoaderCircle" class="spin"/>正在连接回序</div></div>
 <ApiDocs v-else-if="page==='api-docs'&&allowed('api-docs')"/>
 <div v-else-if="!user" class="login-page">
  <section class="login-art"><Brand/>
   <div class="login-copy"><span class="eyebrow">HUIXU / SERVICE DESK</span><h1>让每一笔申请，<br/>都有迹可循<span>。</span></h1><p>提交资料、查看进度、管理费用。<br/>回序，为你的服务流程留下一份清楚的记录。</p>
    <div class="login-process"><div><span>01</span><strong>提交申请</strong><small>单笔录入或批量导入</small></div><div><span>02</span><strong>跟踪进展</strong><small>服务状态持续更新</small></div><div><span>03</span><strong>核对账单</strong><small>费用与记录逐笔对应</small></div></div>
   </div><footer>回序 HUIXU <span>© {{new Date().getFullYear()}}</span></footer>
  </section>
  <section class="login-form"><div><AccountAccess :site="site" :session-error="loginError" @login="loggedIn"/>
   <a v-if="site.menus.some(item=>item.key==='developer')" class="login-doc-link" href="/developers/reference">浏览接口手册<Icon name="ArrowUpRight" :size="14"/></a>
   <DataProtectionNotice v-if="page==='overview'"/>
   <Announcements v-if="site.menus.some(item=>item.key==='announcements')" login :refresh="0"/>
  </div></section>
 </div>
 <div v-else class="app-shell">
  <div v-if="sidebar" class="sidebar-scrim" @click="sidebar=false"/>
  <aside id="workspace-sidebar" class="sidebar" :class="{'is-open':sidebar}" aria-label="回序导航">
   <div class="sidebar-brand-row"><Brand/><button class="icon-button sidebar-close" aria-label="收起导航" @click="sidebar=false"><Icon name="X"/></button></div>
   <div class="sidebar-scroll"><span class="nav-label">服务台 / WORKSPACE</span><nav aria-label="服务导航"><template v-for="item in nav" :key="item.key"><a v-if="item.key.startsWith('custom:')&&item.open_in_new_tab" class="custom-menu-link" :aria-label="item.label" :href="`/workspace/custom/${item.key.slice(7)}/open`" target="_blank" rel="noopener noreferrer"><img v-if="item.icon_url" class="custom-menu-icon" :src="item.icon_url" alt=""/><Icon v-else :name="item.icon"/><span>{{item.label}}</span><Icon name="ArrowUpRight" :size="14"/></a><button v-else :aria-label="item.label" :class="{active:page===item.key}" :aria-current="page===item.key?'page':undefined" @click="go(item.key as PageName)"><img v-if="item.icon_url" class="custom-menu-icon" :src="item.icon_url" alt=""/><Icon v-else :name="item.icon"/><span>{{item.label}}</span><Icon v-if="page===item.key" name="ChevronRight" :size="14"/></button></template></nav>
    <div v-if="allowed('developer')" class="sidebar-tip"><Icon name="Terminal" :size="22"/><strong>把申请交给接口</strong><p>一次接入，串联你的业务流程。</p><button @click="go('developer')">查看接入方式<Icon name="ArrowUpRight" :size="14"/></button></div>
   </div>
   <div class="sidebar-footer"><button v-if="allowed('help')" class="help-link" :class="{active:page==='help'}" @click="go('help')"><Icon name="CircleHelp" :size="18"/>使用指南</button><div class="profile"><div class="avatar">{{initial}}</div><div><strong>{{user.name}}</strong><small>{{user.role==='admin'?'系统管理员':user.role==='operator'?'运营成员':'服务账户'}}</small></div><button class="icon-button" aria-label="退出账户" title="退出账户" @click="logout"><Icon name="LogOut" :size="17"/></button></div></div>
  </aside>
  <div class="main-shell"><header class="topbar">
   <div class="topbar-location"><button class="icon-button mobile-menu" aria-label="展开导航" :aria-expanded="sidebar" aria-controls="workspace-sidebar" @click="sidebar=!sidebar"><Icon name="Menu"/></button><span class="topbar-brand">回序</span><span class="topbar-divider">/</span><strong>{{meta.label}}</strong></div>
   <div class="topbar-actions"><label v-if="allowed('refunds')" class="global-search"><Icon name="Search" :size="16"/><input ref="searchEl" v-model="search" aria-label="查找申请" placeholder="输入邮箱或编号查找" maxlength="100"/><kbd>⌘ K</kbd></label><button v-if="adminMenu" class="topbar-console" :class="{active:page==='admin'}" :aria-label="adminMenu.label" :aria-current="page==='admin'?'page':undefined" @click="go('admin')"><Icon :name="adminMenu.icon" :size="16"/><span>{{adminMenu.label}}</span></button><button class="icon-button notification-button" aria-label="账户消息" :aria-expanded="showNotifications" @click="showNotifications=!showNotifications"><Icon name="Bell" :size="19"/><i v-if="unread"/></button><span class="workspace-avatar">{{initial}}</span></div>
   <div v-if="showNotifications" class="notification-popover"><h3>账户消息</h3><div v-if="notifications.length" class="notification-list"><article v-for="n in notifications" :key="n.id"><span v-if="!n.read_at" class="status-dot"/><p>{{n.message}}</p><small>{{date(n.created_at)}}</small></article></div><p v-else>暂无新消息，服务进展会在这里提醒你。</p><div class="notification-actions"><button v-if="allowed('refunds')" class="text-button" @click="go('refunds')">前往申请记录</button><button v-if="unread" class="text-button" @click="markRead">设为全部已读</button></div></div>
  </header>
  <main class="main-content"><div class="page-heading"><div><span class="eyebrow">{{meta.eyebrow}}</span><h1>{{meta.title}}</h1><p>{{page==='overview'?`${user.name}，欢迎回来。${meta.subtitle}`:meta.subtitle}}</p></div><div v-if="page==='overview'||page==='refunds'" class="heading-actions"><button class="button secondary" aria-label="更新申请数据" :disabled="refreshing" @click="refresh"><Icon name="RefreshCw" :class="{spin:refreshing}"/>{{refreshing?'更新中…':'更新数据'}}</button><button v-if="allowed('refunds')" class="button primary" @click="creating=true"><Icon name="Plus"/>提交申请</button></div><a v-if="page==='developer'" class="button secondary" href="/developers/reference" target="_blank" rel="noopener">打开接口手册<Icon name="ArrowUpRight"/></a></div>
   <div v-if="loadError" class="error-banner" role="alert">{{loadError}}<button @click="refresh">再次连接</button></div>
   <DataProtectionNotice v-if="page==='overview'"/>
   <Announcements v-if="page==='overview'&&allowed('announcements')" compact :refresh="refreshTick" @all="go('announcements')"/>
   <DashboardView v-if="page==='overview'&&allowed('overview')" :user="user" :data="dashboard" :allow-refunds="allowed('refunds')" :allow-billing="allowed('billing')" @create="creating=true" @billing="go('billing')"/>
   <RefundTable v-if="allowed('refunds')&&(page==='overview'||page==='refunds')" :key="page" :overview="page==='overview'" :search="query" :refresh="refreshTick" @detail="detailID=$event" @all="go('refunds')" @create="creating=true" @toast="notify"/>
   <Billing v-if="page==='billing'&&allowed('billing')" :user="user" :refresh="refreshTick" @detail="detailID=$event" @redeemed="notify($event);refresh()"/>
   <Admin v-if="page==='admin'&&adminMenu" :user="user" @toast="notify" @changed="refresh"/>

   <CustomMenuPage v-if="page.startsWith('custom:')&&allowed(page)" :id="page.slice(7)" :refresh="refreshTick"/>
   <Developers v-if="page==='developer'&&allowed('developer')" @toast="notify"/><Announcements v-if="page==='announcements'&&allowed('announcements')" :refresh="refreshTick"/><Help v-if="page==='help'&&allowed('help')" :allow-developer="allowed('developer')"/>
   <footer class="page-footer"><span>回序 HUIXU · 服务有序，进度有据。</span><span><i class="status-dot" :class="{offline:loadError}"/>{{loadError?'连接暂时中断':'数据每 15 秒更新'}}</span></footer>
  </main></div>
  <RefundForm v-if="creating&&allowed('refunds')" :user="user" @close="creating=false" @created="created"/><RefundDetail v-if="detailID&&allowed('refunds')" :key="detailID" :id="detailID" @close="detailID=''" @changed="refresh"/>
 </div>
 <div v-if="toast" class="toast" role="status"><Icon name="Check" :size="16"/>{{toast}}<button aria-label="收起消息" @click="toast=''"><Icon name="X" :size="15"/></button></div>
</template>
