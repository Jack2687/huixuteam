import { ref } from 'vue';
import type { SiteBranding } from './site';

export const branding = ref<SiteBranding>({icon_url:'/api/public/brand-icon',customized:false});
export function applyBranding(value:SiteBranding){
 branding.value=value;
 const icon=document.querySelector<HTMLLinkElement>('link[rel="icon"]');
 if(icon&&icon.getAttribute('href')!==value.icon_url)icon.href=value.icon_url;
}
