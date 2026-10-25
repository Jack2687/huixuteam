<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from "vue";
import Brand from "./components/Brand.vue";
import Announcements from "./components/Announcements.vue";
import Icon from "./components/Icon.vue";
import DashboardView from "./components/Dashboard.vue";
import RefundTable from "./components/RefundTable.vue";
import RefundForm from "./components/RefundForm.vue";
import RefundDetail from "./components/RefundDetail.vue";
import Billing from "./components/Billing.vue";
import Developers from "./components/Developers.vue";
import Help from "./components/Help.vue";
import ApiDocs from "./components/ApiDocs.vue";
const Admin=defineAsyncComponent(()=>import("./components/Admin.vue"));
import {
  api,
  date,
  type User,
  type Dashboard,
  type Notification,
} from "./lib/api";
type PageName = 'overview' | 'refunds' | 'billing' | 'developer' | 'announcements' | 'help' | 'api-docs' | 'admin';
const pages: Record<PageName,{label:string;eyebrow:string;title:string;subtitle:string;icon:string}> = {
 overview:{label:'总览',eyebrow:'01 / OVERVIEW',title:'服务总览',subtitle:'掌握申请进展，安排下一步工作。',icon:'LayoutDashboard'},
 refunds:{label:'申请记录',eyebrow:'02 / REQUESTS',title:'申请记录',subtitle:'从受理到完成，集中查阅每一笔服务记录。',icon:'FileText'},
 billing:{label:'资金与账单',eyebrow:'03 / BALANCE',title:'资金与账单',subtitle:'可用、预留与支出，每一笔变动均可追溯。',icon:'Wallet'},
 developer:{label:'接口接入',eyebrow:'04 / DEVELOPERS',title:'连接你的业务',subtitle:'管理访问凭证，让申请与进度融入你的系统。',icon:'CodeXml'},
 announcements:{label:'公告中心',eyebrow:'05 / BULLETIN',title:'公告中心',subtitle:'关注服务消息与产品更新。',icon:'Bell'},
 help:{label:'使用指南',eyebrow:'GUIDE / SUPPORT',title:'需要一点指引？',subtitle:'先了解申请规则，再开始使用回序。',icon:'CircleHelp'},
 'api-docs':{label:'接口手册',eyebrow:'',title:'',subtitle:'',icon:'CodeXml'},
 admin:{label:'运营控制台',eyebrow:'CONTROL / OPERATIONS',title:'运营控制台',subtitle:'统一管理成员、服务资源与平台内容。',icon:'ShieldCheck'},
};
function route(): PageName {
  const value = location.hash.slice(1);
  return (
    value in pages
      ? value
      : location.pathname === "/developers/reference"
        ? "api-docs"
        : "overview"
  ) as PageName;
}
const page = ref<PageName>(route()),
  meta = computed(() => pages[page.value]);
const nav = computed<PageName[]>(() => [
  "overview",
  "refunds",
  "billing",
  "developer",
  "announcements",
  ...(user.value?.permissions.includes("admin.overview.read")
    ? ["admin" as PageName]
    : []),
]);
const user = ref<User>(),
  booting = ref(true),
  loginEmail = ref(""),
  loginPassword = ref(""),
  loginBusy = ref(false),
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
  sidebar.value = false;
  showNotifications.value = false;
}
function expire() {
  authGeneration++;
  user.value = undefined;
  creating.value = false;
  detailID.value = "";
  notifications.value = [];
  dashboard.value = { counts: {}, trend: [], provider_ready: false };
  loginError.value = "会话已结束，请再次登录。";
}
async function refresh() {
  if (!user.value || refreshPromise) return refreshPromise;
  const generation = authGeneration;
  refreshing.value = true;
  refreshPromise = (async () => {
    try {
      const [u, d, n] = await Promise.all([
        api<User>("/api/auth/me"),
        api<Dashboard>("/api/dashboard"),
        api<{ items: Notification[] }>("/api/notifications"),
      ]);
      if (generation !== authGeneration) return;
      user.value = u;
      dashboard.value = d;
      notifications.value = n.items;
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
async function login() {
  loginBusy.value = true;
  loginError.value = "";
  try {
    user.value = await api<User>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({
        email: loginEmail.value,
        password: loginPassword.value,
      }),
    });
    authGeneration++;
    loginPassword.value = "";
    await refresh();
  } catch (e) {
    loginError.value = (e as Error).message;
  } finally {
    loginBusy.value = false;
  }
}
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
    go("overview");
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
    if (value && page.value !== "refunds") go("refunds");
  }, 250);
});
onMounted(async () => {
  window.addEventListener("hashchange", hashChange);
  window.addEventListener("keydown", keyboard);
  window.addEventListener("session-expired", expire);
  try {
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
 <ApiDocs v-if="page === 'api-docs'" />
 <div v-else-if="booting" class="boot-screen"><Brand/><div class="loading"><Icon name="LoaderCircle" class="spin"/>正在连接回序</div></div>
 <div v-else-if="!user" class="login-page">
  <section class="login-art"><Brand/>
   <div class="login-copy"><span class="eyebrow">HUIXU / SERVICE DESK</span><h1>让每一笔申请，<br/>都有迹可循<span>。</span></h1><p>提交资料、查看进度、管理费用。<br/>回序，为你的服务流程留下一份清楚的记录。</p>
    <div class="login-process"><div><span>01</span><strong>提交申请</strong><small>单笔录入或批量导入</small></div><div><span>02</span><strong>跟踪进展</strong><small>服务状态持续更新</small></div><div><span>03</span><strong>核对账单</strong><small>费用与记录逐笔对应</small></div></div>
   </div><footer>回序 HUIXU <span>© {{new Date().getFullYear()}}</span></footer>
  </section>
  <section class="login-form"><div><span class="eyebrow">ACCOUNT ACCESS</span><h2>登录回序</h2><p>使用已开通的账号进入服务台。</p>
   <form @submit.prevent="login"><label>登录邮箱<input v-model="loginEmail" type="email" autocomplete="username" placeholder="name@example.com" required maxlength="254"/></label><label>账户密码<input v-model="loginPassword" type="password" autocomplete="current-password" placeholder="输入登录密码" required maxlength="72"/></label><p v-if="loginError" class="form-error" role="alert">{{loginError}}</p><button class="button primary full" :disabled="loginBusy">{{loginBusy?'正在验证…':'进入服务台'}}<Icon :name="loginBusy?'LoaderCircle':'ArrowUpRight'" :class="{spin:loginBusy}"/></button></form>
   <p class="login-note"><Icon name="CircleHelp" :size="16"/>还没有账号？请联系服务人员开通。</p><a class="login-doc-link" href="/developers/reference">浏览接口手册<Icon name="ArrowUpRight" :size="14"/></a>
   <Announcements login :refresh="0"/>
  </div></section>
 </div>
 <div v-else class="app-shell">
  <div v-if="sidebar" class="sidebar-scrim" @click="sidebar=false"/>
  <aside id="workspace-sidebar" class="sidebar" :class="{'is-open':sidebar}" aria-label="回序导航">
   <div class="sidebar-brand-row"><Brand/><button class="icon-button sidebar-close" aria-label="收起导航" @click="sidebar=false"><Icon name="X"/></button></div>
   <div class="sidebar-scroll"><span class="nav-label">服务台 / WORKSPACE</span><nav aria-label="服务导航"><button v-for="item in nav" :key="item" :aria-label="pages[item].label" :class="{active:page===item}" :aria-current="page===item?'page':undefined" @click="go(item)"><Icon :name="pages[item].icon"/><span>{{pages[item].label}}</span><Icon v-if="page===item" name="ChevronRight" :size="14"/></button></nav>
    <div class="sidebar-tip"><Icon name="Terminal" :size="22"/><strong>把申请交给接口</strong><p>一次接入，串联你的业务流程。</p><button @click="go('developer')">查看接入方式<Icon name="ArrowUpRight" :size="14"/></button></div>
   </div>
   <div class="sidebar-footer"><button class="help-link" :class="{active:page==='help'}" @click="go('help')"><Icon name="CircleHelp" :size="18"/>使用指南</button><div class="profile"><div class="avatar">{{initial}}</div><div><strong>{{user.name}}</strong><small>{{user.role==='admin'?'系统管理员':user.role==='operator'?'运营成员':'服务账户'}}</small></div><button class="icon-button" aria-label="退出账户" title="退出账户" @click="logout"><Icon name="LogOut" :size="17"/></button></div></div>
  </aside>
  <div class="main-shell"><header class="topbar">
   <div class="topbar-location"><button class="icon-button mobile-menu" aria-label="展开导航" :aria-expanded="sidebar" aria-controls="workspace-sidebar" @click="sidebar=!sidebar"><Icon name="Menu"/></button><span class="topbar-brand">回序</span><span class="topbar-divider">/</span><strong>{{meta.label}}</strong></div>
   <div class="topbar-actions"><label class="global-search"><Icon name="Search" :size="16"/><input ref="searchEl" v-model="search" aria-label="查找申请" placeholder="输入邮箱或编号查找" maxlength="100"/><kbd>⌘ K</kbd></label><button class="icon-button notification-button" aria-label="账户消息" :aria-expanded="showNotifications" @click="showNotifications=!showNotifications"><Icon name="Bell" :size="19"/><i v-if="unread"/></button><span class="workspace-avatar">{{initial}}</span></div>
   <div v-if="showNotifications" class="notification-popover"><h3>账户消息</h3><div v-if="notifications.length" class="notification-list"><article v-for="n in notifications" :key="n.id"><span v-if="!n.read_at" class="status-dot"/><p>{{n.message}}</p><small>{{date(n.created_at)}}</small></article></div><p v-else>暂无新消息，服务进展会在这里提醒你。</p><div class="notification-actions"><button class="text-button" @click="go('refunds')">前往申请记录</button><button v-if="unread" class="text-button" @click="markRead">设为全部已读</button></div></div>
  </header>
  <main class="main-content"><div class="page-heading"><div><span class="eyebrow">{{meta.eyebrow}}</span><h1>{{meta.title}}</h1><p>{{page==='overview'?`${user.name}，欢迎回来。${meta.subtitle}`:meta.subtitle}}</p></div><div v-if="page==='overview'||page==='refunds'" class="heading-actions"><button class="button secondary" aria-label="更新申请数据" :disabled="refreshing" @click="refresh"><Icon name="RefreshCw" :class="{spin:refreshing}"/>{{refreshing?'更新中…':'更新数据'}}</button><button class="button primary" @click="creating=true"><Icon name="Plus"/>提交申请</button></div><a v-if="page==='developer'" class="button secondary" href="/developers/reference" target="_blank" rel="noopener">打开接口手册<Icon name="ArrowUpRight"/></a></div>
   <div v-if="loadError" class="error-banner" role="alert">{{loadError}}<button @click="refresh">再次连接</button></div>
   <Announcements v-if="page==='overview'" compact :refresh="refreshTick" @all="go('announcements')"/>
   <DashboardView v-if="page==='overview'" :user="user" :data="dashboard" @create="creating=true" @billing="go('billing')"/>
   <RefundTable v-if="page==='overview'||page==='refunds'" :key="page" :overview="page==='overview'" :search="query" :refresh="refreshTick" @detail="detailID=$event" @all="go('refunds')" @create="creating=true" @toast="notify"/>
   <Billing v-if="page==='billing'" :user="user" :refresh="refreshTick" @detail="detailID=$event" @redeemed="notify($event);refresh()"/>
   <Admin v-if="page==='admin'&&user.permissions.includes('admin.overview.read')" :user="user" @toast="notify" @changed="refresh"/>
   <div v-else-if="page==='admin'" class="notice">当前账户无法访问运营控制台。</div>
   <Developers v-if="page==='developer'" @toast="notify"/><Announcements v-if="page==='announcements'" :refresh="refreshTick"/><Help v-if="page==='help'"/>
   <footer class="page-footer"><span>回序 HUIXU · 服务有序，进度有据。</span><span><i class="status-dot" :class="{offline:loadError}"/>{{loadError?'连接暂时中断':'数据每 15 秒更新'}}</span></footer>
  </main></div>
  <RefundForm v-if="creating" :user="user" @close="creating=false" @created="created"/><RefundDetail v-if="detailID" :key="detailID" :id="detailID" @close="detailID=''" @changed="refresh"/>
 </div>
 <div v-if="toast" class="toast" role="status"><Icon name="Check" :size="16"/>{{toast}}<button aria-label="收起消息" @click="toast=''"><Icon name="X" :size="15"/></button></div>
</template>
