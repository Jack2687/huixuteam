export type RiskRule = {mode:'off'|'observe'|'enforce';limit:number;window_seconds:number;cooldown_seconds:number};
export type RiskSettings = {
 enabled:boolean;rules:Record<string,RiskRule>;retention_days:number;refresh_seconds:number;scan_seconds:number;
 behavior_window_seconds:number;login_source_accounts:number;duplicate_count:number;cancel_count:number;failure_min_count:number;failure_percent:number;
 queue_count:number;queue_seconds:number;processing_seconds:number;large_adjustment:number;finance_checks:boolean;trusted_proxies:string[];revision:number;
};
export const ruleNames:Record<string,string>={login:'登录尝试',redeem:'兑换码兑换',submit:'提交申请',query:'查询与轮询',write:'其他业务操作',account:'客户总请求量',api_key:'单个 API 密钥'};
export const operationNames:Record<string,string>={login:'账户登录',logout:'退出登录',submit:'提交申请',query:'业务查询',cancel:'撤回申请',redeem:'兑换积分',keys:'密钥管理',admin:'后台管理',risk:'风控管理',resources:'服务配置',unknown:'其他业务访问'};
export const categoryNames:Record<string,string>={login:'登录安全',access:'接口访问',limit:'访问限制',application:'申请异常',finance:'积分与兑换',admin:'管理操作'};
export type RiskControl={user_id:string;paused:boolean;paused_until:string|null;limits:Record<string,number>};
