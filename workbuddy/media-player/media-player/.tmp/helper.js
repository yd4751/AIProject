const m="内容可能被浏览器误拦截，请关闭广告拦截功能，或换个浏览器重试！";function h(t,{requireText:n=!1}={}){if(!t?.isConnected||n&&!t.textContent.trim())return!0;const e=t.getBoundingClientRect();if(e.width<=0||e.height<=0)return!0;const r=t.ownerDocument.defaultView;for(let i=t;i;i=i.parentElement){const a=r.getComputedStyle(i);if(a.display==="none"||a.visibility==="hidden"||a.visibility==="collapse"||Number(a.opacity)===0)return!0}return!1}function v(t=m){window.alert(t)}function w(t,{delay:n=800,interval:e=1e3,requireText:r=!1,message:i=m,onBlocked:a=(l,o)=>v(o)}={}){let l=!1,o;const c=()=>{l=!0,clearTimeout(o)};function u(){if(!l){if(document.visibilityState!=="hidden"){const s=typeof t=="function"?t():typeof t=="string"?document.querySelector(t):t;if(h(s,{requireText:r})){c(),a(s,i);return}}o=setTimeout(u,e)}}return o=setTimeout(u,n),c}function y(){const t=navigator.userAgent.toLowerCase(),n={wechat:"微信进入的用户请换用系统自带浏览器重新打开本站才能进行歌曲下载",qq:"QQ进入的用户请换用系统自带浏览器重新打开本站才能进行歌曲下载",tieba:"百度贴吧进入的用户请换用系统自带浏览器重新打开本站才能进行歌曲下载"};return t.match(/MicroMessenger\/[0-9]/i)?(jAlert(n.wechat),!0):t.match(/QQ\/[0-9]/i)?(jAlert(n.qq),!0):t.match(/tieba\/[0-9]/i)?(jAlert(n.tieba),!0):!1}function g(t,n){$.ajax({url:"/api/map-feedback",type:"POST",dataType:"json",data:{id:t,type:n}})}async function b(t){return await new Promise(n=>{if(t<=0){n(!0);return}let e=null,r=null,i=!1;const a="/member/vip-buy",l=`
      <div class="text-center pt-2 pb-1">
        <h6 class="font-weight-bold text-secondary mb-4">
            <i class="fa fa-hourglass-half fa-spin text-warning mr-1"></i> 普通通道拥挤，排队解析中...
        </h6>

        <div class="wait-countdown-wrapper mb-3">
          <span style="font-size: 46px; color: #ff7a45; font-weight: 800; line-height: 1;" id="download-countdown">
            ${t}
          </span>
        </div>

        <div class="text-muted mt-2 mb-4" style="font-size: 13px;">秒后自动为您获取下载链接</div>

        <div class="p-3 rounded text-left shadow-sm vip-privilege-banner">
          <div class="d-flex align-items-center mb-1">
            <span class="diamond-icon-anim text-orange">
                <i class="fa fa-diamond" style="font-size: 1.2rem;"></i>
            </span>
            <span class="text-dark ml-2 font-weight-bolder" style="font-size: 15px;">开通 VIP 特权</span>
          </div>
          <div class="mt-1" style="color: #855800; font-size: 13px; line-height: 1.6;">
            解锁专属解析通道，<strong>免排队零等待</strong>，以及更多 VIP 特权等你体验！
          </div>
        </div>
      </div>
    `;jAlert(l,"orange",!1,{title:!1,columnClass:"col-md-5 col-lg-4 col-sm-10",buttons:{vip:{text:"开通VIP，跳过等待",btnClass:"vip-upgrade-cta font-weight-bold w-100 rounded-pill",action:function(){return e&&clearInterval(e),window.location.href=a,!0}}},onContentReady:function(){const o=this;o.$content.find(".vip-privilege-banner").on("click",function(){e&&clearInterval(e),window.location.href=a}),e=setInterval(()=>{t=Math.max(0,t-1),o.$content.find("#download-countdown").text(t),t<=0&&(e&&clearInterval(e),i=!0,o.close())},1e3)},onOpen:function(){r=w(()=>this.$content[0]?.querySelector("#download-countdown"),{requireText:!0,message:m+"或开通VIP可快捷下载~"})},onClose:function(){r?.()},onDestroy:function(){r?.(),e&&clearInterval(e),n(i)}})})}function x(t){if(!t)return null;const n=t.querySelector(".vip-flip-card__value");if(!n)return s=>{t.textContent=String(s)};const e=t.querySelector(".vip-flip-card__previous"),r=t.querySelector(".vip-flip-card__top"),i=t.querySelector(".vip-flip-card__bottom"),a=t.ownerDocument,l=a.defaultView;let o=null,c=[];const u=function(){c.forEach(s=>{s.onfinish=null,s.cancel()}),c=[],t.classList.remove("is-flipping")};return function(s){const f=String(s);if(f===o)return;const d=o;if(o=f,u(),n.textContent=f,!(d===null||a.hidden||t.closest("[hidden]")||!e||!r||!i||typeof r.animate!="function"))try{if(l.matchMedia?.("(prefers-reduced-motion: reduce)").matches)return;e.textContent=d,r.textContent=d,i.textContent=f,t.classList.add("is-flipping"),c.push(r.animate([{transform:"rotateX(0deg)",opacity:1},{transform:"rotateX(-90deg)",opacity:.7}],{duration:150,easing:"ease-in",fill:"forwards"}));const p=i.animate([{transform:"rotateX(90deg)",opacity:.7},{transform:"rotateX(0deg)",opacity:1}],{duration:150,delay:150,easing:"ease-out",fill:"both"});c.push(p),p.onfinish=function(){c.includes(p)&&u()}}catch{u()}}}export{b as a,x as c,y as i,g as m};
