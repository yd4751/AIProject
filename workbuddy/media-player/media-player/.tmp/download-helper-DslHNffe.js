function y(n,l=1,s=0,d=1){$.post("/member/music/download-log",{music_id:n,link_type:l,extra_url_id:s,source:d})}window.downloadLog=y;function q(n,l,s="",d=1){return y(n,2,l.id,d),l.compel_wap?($.alert({title:"",content:`
<div class="d-flex justify-content-center flex-column align-items-center font-weight-bolder">
<div id="extra-compel-url-qrcode-code" class="my-1"></div>
<p class="mb-4">扫描二维码下载</p>
<p><span class="badge badge-light-orange" style="font-size: 1rem">推荐</span> 使用手机 <span class="text-danger">浏览器</span> 扫描上方二维码</p>
<p class="text-muted">方法2: 手机访问 ${window.location.hostname} 直接下载</p>
</div>
`,closeIcon:!0,typeAnimated:!0,backgroundDismiss:!1,buttons:!1,columnClass:"col-md-4",onContentReady:function(){setTimeout(()=>{$.fn.qrcode?$("#extra-compel-url-qrcode-code").qrcode({width:150,height:150,background:"#ffffff",foreground:"#000000",text:atob(l.share_link)}):jAlert("静态资源加载失败，请刷新重试")},0)}}),!0):jumpTo(atob(l.share_link),s)}function D({extraUrls:n=[],playUrl:l="",musicId:s,musicName:d,extraRecommendWapUrl:m="",extraUrlCompel:b=!1,isVip:k=!1,vipDownUrl:u=!1,downloadSource:g=1,onNormalDownload:i=null}){const t=n.length>0,C=b&&t&&n.length===1&&!user.config?.alwaysShowNormalDownChannel,h=!!l||typeof i=="function",w=h&&(!b||!t||user.config?.alwaysShowNormalDownChannel);if(C)return q(s,n[0],"",g);if(!t&&!h&&!u)return!1;let o="";t&&w&&(o+=`
<div class="alert alert-warning border-0 bg-soft-orange text-dark font-sm mb-3 shadow-sm rounded">
    <i class="fa fa-diamond text-orange mr-1"></i>
    建议使用 <span class="trigger-focus-recommend cursor-pointer font-weight-bolder">推荐通道</span> ，音质好&保存后支持跨设备播放与下载
</div>`),o+='<div class="download-list">';let c=1;for(let e=0;e<n.length;e++){const a=n[e],r=a.pwd?`<p class="mt-1 mb-0 text-muted d-flex align-items-center" style="cursor: copy;" title="点击全选提取码"
                 onclick="event.preventDefault(); event.stopPropagation(); let s=window.getSelection(), r=document.createRange(); r.selectNodeContents(this.querySelector('.pwd-text')); s.removeAllRanges(); s.addRange(r);">
           提取码：
           <span class="pwd-text font-weight-bolder text-info bg-light border px-2 rounded"
                 style="letter-spacing: 1px;">
               ${a.pwd}
           </span>
       </p>`:"";o+=`
<a href="/dp/${a.share_link}" target="_blank" class="download-option-card extra-link" data-index="${e}">
    <span class="ribbon-badge ribbon-recommend">推荐</span>
    <div class="download-icon-box bg-soft-blue text-primary">
        <i class="fa fa-cloud-download"></i>
    </div>
    <div class="download-info flex-grow-1">
        <div class="d-flex align-items-center">
            <h6 class="text-highlight-blue">
                <span class="font-weight-bold mr-1" style="opacity:0.6;">#${c}</span> 下载高品质MP3
            </h6>
            <span class="badge badge-pill badge-light-blue ml-2 font-weight-normal" style="transform: scale(0.9)">高音质</span>
        </div>
        <p class="mt-1 text-muted d-flex align-items-center">
            存储于：
            <span class="font-weight-bolder">${a.type}</span> &nbsp;
            <span style="border-left: 1px solid #eee; padding-left: 4px;">音质好·稳定·免费</span>
        </p>
        ${r}
    </div>
    <div class="text-gray">
        <i class="fa fa-angle-right"></i>
    </div>
</a>`,c++}if(w){const e=t?'<span class="ribbon-badge ribbon-backup">备用</span>':"",a=t?"bg-light text-secondary":"bg-soft-blue text-primary",r=t?"fa-arrow-circle-o-down":"fa-download",p=t?"text-secondary":"text-highlight-blue",f=t?'<span class="badge badge-pill badge-light-grey ml-2 font-weight-normal" style="transform: scale(0.9)">低音质</span>':"",v=t?"下载低品质MP3":"直链下载通道",x=t?"备用线路 (音质差,不稳定,不建议使用)":"直接将歌曲保存到本地设备";o+=`
<a href="${l||"#"}"
   download="${d}.mp3"
   class="download-option-card default-link"
   target="_blank"
   ${i?'data-normal-download="1"':`onclick="downloadLog(${s},1,0,${g})"`}>
    ${e}
    <div class="download-icon-box ${a}">
        <i class="fa ${r}"></i>
    </div>
    <div class="download-info flex-grow-1">
        <div class="d-flex align-items-center">
            <h6 class="${p} mb-0">
                <span class="font-weight-bold mr-1" style="opacity:0.6;">#${c}</span> ${v}
            </h6>
            ${f}
        </div>
        <p class="mt-1 text-muted">${x}</p>
    </div>
    <div class="text-gray">
        <i class="fa fa-angle-right"></i>
    </div>
</a>`,c++,k&&u&&(o+=`
<a href="${i?"#":`/member/music/download?u=${encodeURIComponent(l)}&n=${encodeURIComponent(d)}.mp3`}"
   class="download-option-card default-link"
   target="_blank"
   ${i?'data-normal-download="3"':`onclick="downloadLog(${s},3,0,${g})"`}>
    ${e}
    <div class="download-icon-box ${a}">
        <i class="fa ${r}"></i>
    </div>
    <div class="download-info flex-grow-1">
        <div class="d-flex align-items-center">
            <h6 class="${p} mb-0">
                <span class="font-weight-bold mr-1" style="opacity:0.6;">#${c}</span> ${v}
            </h6>
            ${f}
        </div>
        <p class="mt-1 text-muted">${x}</p>
    </div>
    <div class="text-gray">
        <i class="fa fa-angle-right"></i>
    </div>
</a>`,c++)}o+="</div>",m&&(o+=`
<div class="d-flex align-items-center p-3 rounded-lg mt-3" style="background-color: #f8f9fa; border: 1px dashed #e0e0e0;">
    <div class="mr-3 bg-white p-1 rounded border shadow-sm" id="extra-recommend-url-qrcode-code" style="width: 104px; height: 104px;">
    </div>
    <div class="flex-grow-1 d-flex flex-column justify-content-center font-weight-bold text-dark" style="height: 104px;">
        <div class="mb-2">使用手机 <span class="text-cyan">浏览器</span> 「扫一扫」</div>
        <div>在手机上保存获得更好体验</div>
    </div>
</div>`);const _=t?"请选择音质":"请选择通道";return $.alert({title:" ",titleClass:"d-none",content:`
<div class="pt-3 px-1">
    <h5 class="font-weight-bold mb-3 text-center">${_}</h5>
    ${o}
</div>`,closeIcon:!0,typeAnimated:!0,backgroundDismiss:!1,buttons:!1,columnClass:"col-md-5 col-sm-12",animateFromElement:!1,onContentReady:function(){typeof i=="function"&&this.$content.find("[data-normal-download]").each(function(){const e=this;let a=!1;$(e).off("click.normalDownload").on("click.normalDownload",function(r){a||(r.preventDefault(),i(Number($(e).attr("data-normal-download")),e.target,function(f){if(f){e.href=f,a=!0;try{e.click()}finally{a=!1}}}))})}),m&&setTimeout(()=>{$.fn.qrcode&&$("#extra-recommend-url-qrcode-code").qrcode({width:96,height:96,background:"#ffffff",foreground:"#000000",text:atob(m)})},0)}}),!0}function R(){if($("#ad-modal").length)return;$("body").append(`
<div class="modal fade" id="ad-modal" tabindex="-1" role="dialog" aria-labelledby="ad-modal-title" aria-hidden="true">
    <div class="modal-dialog modal-lg" role="document">
        <div class="modal-content">
            <div class="modal-header align-items-center">
                <h5 class="modal-title badge bg-success text-white" id="ad-modal-title">温馨提示</h5>
                <span class="font-weight-bolder text-danger ml-1" id="ad-modal-prompt"></span>
                <button type="button" class="close" data-dismiss="modal" aria-label="Close">
                    <span aria-hidden="true">&times;</span>
                </button>
            </div>
            <div class="modal-body d-flex justify-content-center" id="ad-modal-prepend"></div>
            <div class="modal-body" id="ad-modal-body"></div>
        </div>
    </div>
</div>`)}export{q as a,D as b,y as d,R as e};
