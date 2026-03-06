(function () {
  if (window.InlineChatWidget && window.InlineChatWidget.__mounted) {
    return;
  }

  var script = document.currentScript;
  if (!script) {
    return;
  }

  var siteId = (script.dataset.siteId || "").trim();
  if (!siteId) {
    console.error("[InlineChat] 缺少 data-site-id，无法初始化客服组件。");
    return;
  }

  var scriptURL = new URL(script.src, window.location.href);
  var gatewayOrigin = (script.dataset.gatewayOrigin || scriptURL.origin).trim();
  var title = (script.dataset.title || "在线客服").trim();
  var primaryColor = (script.dataset.primaryColor || "#2f343c").trim();
  var bottom = toPx(script.dataset.bottom, "24px");
  var right = toPx(script.dataset.right, "24px");
  var zIndex = parseInt(script.dataset.zIndex || "2147483000", 10);
  var panelWidth = toPx(script.dataset.panelWidth, "380px");
  var panelHeight = toPx(script.dataset.panelHeight, "620px");

  var frameURL = new URL(gatewayOrigin + "/app/widget/");
  var parentOrigin = window.location.origin && window.location.origin !== "null" ? window.location.origin : "*";
  frameURL.searchParams.set("site_id", siteId);
  frameURL.searchParams.set("title", title);
  frameURL.searchParams.set("parent_origin", parentOrigin);

  var host = document.createElement("div");
  host.setAttribute("data-inlinechat-host", "true");
  host.style.position = "fixed";
  host.style.right = right;
  host.style.bottom = bottom;
  host.style.zIndex = String(Number.isNaN(zIndex) ? 2147483000 : zIndex);
  host.style.display = "flex";
  host.style.flexDirection = "column";
  host.style.alignItems = "flex-end";
  host.style.gap = "12px";

  var panel = document.createElement("div");
  panel.style.width = panelWidth;
  panel.style.height = panelHeight;
  panel.style.maxWidth = "calc(100vw - 16px)";
  panel.style.maxHeight = "calc(100vh - 88px)";
  panel.style.border = "1px solid rgba(0,0,0,0.12)";
  panel.style.borderRadius = "18px";
  panel.style.overflow = "hidden";
  panel.style.boxShadow = "0 24px 50px rgba(15, 23, 42, 0.24)";
  panel.style.background = "#fff";
  panel.style.display = "none";

  var iframe = document.createElement("iframe");
  iframe.title = title;
  iframe.src = frameURL.toString();
  iframe.style.width = "100%";
  iframe.style.height = "100%";
  iframe.style.border = "0";
  iframe.allow = "clipboard-write";
  iframe.referrerPolicy = "origin";
  panel.appendChild(iframe);

  var button = document.createElement("button");
  button.type = "button";
  button.setAttribute("aria-label", title);
  button.style.width = "56px";
  button.style.height = "56px";
  button.style.borderRadius = "999px";
  button.style.border = "0";
  button.style.display = "grid";
  button.style.placeItems = "center";
  button.style.cursor = "pointer";
  button.style.color = "#fff";
  button.style.background = primaryColor;
  button.style.boxShadow = "0 12px 24px rgba(0,0,0,0.28)";
  button.style.transition = "transform 0.2s ease";
  button.innerHTML = iconSVG();

  button.addEventListener("mouseenter", function () {
    button.style.transform = "translateY(-1px) scale(1.02)";
  });
  button.addEventListener("mouseleave", function () {
    button.style.transform = "none";
  });

  var isOpen = false;
  function openPanel() {
    panel.style.display = "block";
    isOpen = true;
  }

  function closePanel() {
    panel.style.display = "none";
    isOpen = false;
  }

  function togglePanel() {
    if (isOpen) {
      closePanel();
      return;
    }
    openPanel();
  }

  button.addEventListener("click", togglePanel);

  function onMessage(event) {
    if (event.origin !== gatewayOrigin) {
      return;
    }
    if (!event.data || typeof event.data !== "object") {
      return;
    }
    if (event.data.type === "inlinechat.widget.close") {
      closePanel();
    }
    if (event.data.type === "inlinechat.widget.open") {
      openPanel();
    }
  }

  window.addEventListener("message", onMessage);

  host.appendChild(panel);
  host.appendChild(button);
  document.body.appendChild(host);

  window.InlineChatWidget = {
    __mounted: true,
    open: openPanel,
    close: closePanel,
    toggle: togglePanel,
    destroy: function () {
      window.removeEventListener("message", onMessage);
      host.remove();
      window.InlineChatWidget = undefined;
    },
  };

  function toPx(input, fallback) {
    var v = (input || "").trim();
    if (!v) {
      return fallback;
    }
    if (/^[0-9]+(\.[0-9]+)?$/.test(v)) {
      return v + "px";
    }
    return v;
  }

  function iconSVG() {
    return (
      '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">' +
      '<path d="M4 13a8 8 0 1 1 16 0v5a2 2 0 0 1-2 2h-1v-7h1a1 1 0 0 0 1-1 7 7 0 1 0-14 0 1 1 0 0 0 1 1h1v7H6a2 2 0 0 1-2-2v-5Z" fill="currentColor" opacity="0.92"/>' +
      '<rect x="7" y="12" width="3" height="7" rx="1.2" fill="white"/>' +
      '<rect x="14" y="12" width="3" height="7" rx="1.2" fill="white"/>' +
      '<path d="M10 19c0 1.2 1.1 2 2.3 2H14" stroke="white" stroke-width="1.6" stroke-linecap="round"/>' +
      '<circle cx="14.8" cy="21" r="1.1" fill="white"/>' +
      "</svg>"
    );
  }
})();
