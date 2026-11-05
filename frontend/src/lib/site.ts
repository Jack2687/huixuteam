export interface SiteMenu { key:string; label:string; icon:string; icon_url?:string; open_in_new_tab?:boolean }
export interface CustomMenu {
 id:string; label:string; url:string; icon_svg:string; visibility:'user'|'admin';
 enabled:boolean; open_in_new_tab:boolean; hide_open_button:boolean; sort_order:number;
}
export interface SiteBranding { icon_url:string; customized:boolean }
export interface PublicSite {
 branding:SiteBranding;
 menus:SiteMenu[];
 registration_enabled:boolean;
 email_verification_enabled:boolean;
 email_domains:string[];
 password_reset_enabled:boolean;
}
