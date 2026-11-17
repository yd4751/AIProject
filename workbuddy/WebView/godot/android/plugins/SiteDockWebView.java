package com.sitedock.webview;

import android.app.Activity;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;

import androidx.annotation.NonNull;

import org.godotengine.godot.Godot;
import org.godotengine.godot.plugin.GodotPlugin;
import org.godotengine.godot.plugin.UsedByGodot;

/**
 * Godot 4 (v2 plugin format) —— 通用网页视图。
 *
 * 用系统 android.webkit.WebView 原样渲染任意网页：执行 JS、支持 SPA、播放媒体，
 * 完全不做 HTML 抓取 / 解析 / 抽文字。Godot 侧只传一个 URL 进来。
 *
 * 状态：源码骨架，尚未编译、未在真机验证（本机缺 JDK 17 与 Android SDK）。
 * 类签名、生命周期与打包方式请以 Godot 官方文档当前版本为准。
 * 打包成 AAR 后放到 res://android/plugins/ 并在导出预设中启用。
 */
public class SiteDockWebView extends GodotPlugin {

    private final Activity activity;
    private WebView webView;

    public SiteDockWebView(Godot godot) {
        super(godot);
        this.activity = getActivity();
    }

    @NonNull
    @Override
    public String getPluginName() {
        return "SiteDockWebView";
    }

    /** 加载任意 URL；首次调用时创建 WebView 并叠到 Godot 视图之上。 */
    @UsedByGodot
    public void showUrl(final String url) {
        activity.runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (webView == null) {
                    webView = new WebView(activity);
                    WebSettings settings = webView.getSettings();
                    settings.setJavaScriptEnabled(true);
                    settings.setDomStorageEnabled(true);
                    settings.setLoadWithOverviewMode(true);
                    settings.setUseWideViewPort(true);
                    settings.setMediaPlaybackRequiresUserGesture(false);
                    webView.setWebViewClient(new WebViewClient());
                    ViewGroup content = activity.findViewById(android.R.id.content);
                    content.addView(webView, new FrameLayout.LayoutParams(
                            FrameLayout.LayoutParams.MATCH_PARENT,
                            FrameLayout.LayoutParams.MATCH_PARENT));
                }
                webView.setVisibility(View.VISIBLE);
                webView.bringToFront();
                webView.loadUrl(url);
            }
        });
    }

    /**
     * 限定网页区域，避免遮住 Godot 的顶栏与底部 Tab。
     * 阶段二由 GDScript 按 viewport 实际尺寸换算后调用；当前预览版尚未接线。
     */
    @UsedByGodot
    public void setRect(final int left, final int top, final int width, final int height) {
        activity.runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (webView == null) {
                    return;
                }
                FrameLayout.LayoutParams lp =
                        (FrameLayout.LayoutParams) webView.getLayoutParams();
                if (lp == null) {
                    lp = new FrameLayout.LayoutParams(width, height);
                }
                lp.width = width;
                lp.height = height;
                lp.leftMargin = left;
                lp.topMargin = top;
                webView.setLayoutParams(lp);
            }
        });
    }

    /** 返回仪表盘时隐藏网页视图。 */
    @UsedByGodot
    public void hide() {
        activity.runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (webView != null) {
                    webView.setVisibility(View.GONE);
                }
            }
        });
    }

    /** WebView 内部返回上一页。 */
    @UsedByGodot
    public void back() {
        activity.runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (webView != null && webView.canGoBack()) {
                    webView.goBack();
                }
            }
        });
    }

    /** 退出前释放；由 GDScript 在退出流程调用。 */
    @UsedByGodot
    public void destroy() {
        activity.runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (webView != null) {
                    webView.destroy();
                    webView = null;
                }
            }
        });
    }
}
