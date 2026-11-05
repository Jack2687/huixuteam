export interface SiteMenu { key:string; label:string; icon:string }
export interface PublicSite {
 menus:SiteMenu[];
 registration_enabled:boolean;
 email_verification_enabled:boolean;
 email_domains:string[];
 password_reset_enabled:boolean;
}
