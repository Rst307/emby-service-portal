"use strict";

/* ============================================================
   复制邀请码（data-copy="要复制的文本"）
   ============================================================ */

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-copy]");
  if (!button) return;
  const code = button.dataset.copy;
  const fallbackCopy = () => {
    const input = document.createElement("textarea");
    input.value = code;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    document.execCommand("copy");
    input.remove();
  };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(code).catch(fallbackCopy);
  } else {
    fallbackCopy();
  }
  const original = button.textContent;
  button.textContent = "已复制";
  button.disabled = true;
  window.setTimeout(() => {
    button.textContent = original;
    button.disabled = false;
  }, 1500);
});

/* ============================================================
   危险操作确认（form[data-confirm]，替代被 CSP 拦截的内联 onsubmit）
   ============================================================ */

document.addEventListener("submit", (event) => {
  const form = event.target.closest("form[data-confirm]");
  if (!form) return;
  if (!window.confirm(form.dataset.confirm)) {
    event.preventDefault();
  }
});

/* ============================================================
   编辑账号弹窗（账号列表页）
   ============================================================ */

(() => {
  const dialog = document.getElementById("edit-account-dialog");
  if (!dialog || typeof dialog.showModal !== "function") return;

  const form = document.getElementById("edit-account-form");
  const username = document.getElementById("edit-username");
  const version = document.getElementById("edit-version");
  const original = document.getElementById("edit-expires-original");
  const expiresAt = document.getElementById("edit-expires-at");
  const note = document.getElementById("edit-note");

  const openDialog = (button) => {
    form.action = "/admin/accounts/" + button.dataset.id + "/update";
    version.value = button.dataset.version || "";
    original.value = button.dataset.expiresAtOriginal || "";
    expiresAt.value = button.dataset.expiresAt || "";
    note.value = button.dataset.note || "";
    if (username) username.textContent = button.dataset.username || "";
    dialog.showModal();
    expiresAt.focus();
  };

  document.querySelectorAll("[data-edit-account]").forEach((button) => {
    button.addEventListener("click", () => openDialog(button));
  });

  dialog.querySelectorAll("[data-close-dialog]").forEach((button) => {
    button.addEventListener("click", () => dialog.close());
  });

  // 点击遮罩关闭
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
})();

/* ============================================================
   显示时区设置（设置页）
   ============================================================ */

(() => {
  const select = document.querySelector("[data-time-zone-select]");
  const customField = document.querySelector("[data-custom-time-zone]");
  if (!select || !customField) return;
  const sync = () => {
    customField.hidden = select.value !== "__custom__";
  };
  select.addEventListener("change", sync);
  sync();
})();

/* ============================================================
   批量账号操作（账号列表页）
   ============================================================ */

(() => {
  const form = document.getElementById("account-batch");
  if (!form) return;

  const action = form.querySelector("[data-batch-action]");
  const durationFields = form.querySelectorAll("[data-batch-duration]");
  const durationInput = form.querySelector("input[name=duration]");
  const expiryField = form.querySelector("[data-batch-expiry]");
  const expiryInput = form.querySelector("input[name=expires_at]");
  const selectAll = document.querySelector("[data-batch-select-all]");
  const statusFilter = form.querySelector("[data-batch-status-filter]");
  const selectStatus = form.querySelector("[data-batch-select-status]");
  const clearSelection = form.querySelector("[data-batch-clear]");
  const selection = form.querySelector("[data-batch-selection]");

  let submitFailed = false;

  const checkboxes = () => [...document.querySelectorAll(".batch-select")];
  const visibleCheckboxes = () =>
    checkboxes().filter((box) => !box.closest("[data-account-row]").hidden);

  const updateOperationFields = () => {
    const isDuration = action.value === "extend" || action.value === "reduce";
    const isExpiry = action.value === "set_expiry";
    durationFields.forEach((field) => { field.hidden = !isDuration; });
    expiryField.hidden = !isExpiry;
    durationInput.required = isDuration;
    expiryInput.required = isExpiry;
  };

  const updateVisibility = () => {
    const filter = statusFilter ? statusFilter.value : "all";
    document.querySelectorAll("[data-account-row]").forEach((row) => {
      row.hidden = filter !== "all" && row.dataset.accountStatus !== filter;
    });
  };

  const updateSelection = () => {
    const all = checkboxes();
    const visible = visibleCheckboxes();
    const checked = all.filter((box) => box.checked).length;
    const visibleChecked = visible.filter((box) => box.checked).length;
    selection.textContent = checked ? `已选择 ${checked} 个账号` : "尚未选择账号";
    selection.classList.toggle("error", submitFailed && checked === 0);
    if (selectAll) {
      selectAll.checked = visible.length > 0 && visibleChecked === visible.length;
      selectAll.indeterminate = visibleChecked > 0 && visibleChecked < visible.length;
    }
  };

  const markChanged = () => {
    submitFailed = false;
    updateSelection();
  };

  action.addEventListener("change", updateOperationFields);

  if (statusFilter) {
    statusFilter.addEventListener("change", () => {
      // 切换筛选时取消已隐藏行的勾选，避免误操作当前不可见的账号
      const filter = statusFilter.value;
      checkboxes().forEach((box) => {
        const row = box.closest("[data-account-row]");
        if (filter !== "all" && row.dataset.accountStatus !== filter) box.checked = false;
      });
      updateVisibility();
      markChanged();
    });
  }

  checkboxes().forEach((box) => box.addEventListener("change", markChanged));

  if (selectAll) {
    selectAll.addEventListener("change", () => {
      visibleCheckboxes().forEach((box) => { box.checked = selectAll.checked; });
      markChanged();
    });
  }

  if (selectStatus) {
    selectStatus.addEventListener("click", () => {
      visibleCheckboxes().forEach((box) => { box.checked = true; });
      markChanged();
    });
  }

  if (clearSelection) {
    clearSelection.addEventListener("click", () => {
      checkboxes().forEach((box) => { box.checked = false; });
      markChanged();
    });
  }

  form.addEventListener("submit", (event) => {
    const checked = checkboxes().filter((box) => box.checked).length;
    if (!checked) {
      event.preventDefault();
      submitFailed = true;
      selection.textContent = "请至少选择一个账号";
      selection.classList.add("error");
      return;
    }
    if (!window.confirm(`确认对 ${checked} 个账号执行此操作？`)) event.preventDefault();
  });

  updateOperationFields();
  updateVisibility();
  updateSelection();
})();

/* ============================================================
   支付订单状态轮询与激活码复制
   ============================================================ */

(() => {
  const page = document.querySelector("[data-payment-page]");
  if (!page) return;

  const token = page.dataset.paymentToken;
  const state = page.querySelector("[data-payment-state]");
  const hint = page.querySelector("[data-payment-hint]");
  const result = page.querySelector("[data-payment-result]");
  const code = page.querySelector("[data-activation-code]");
  const paymentLink = page.querySelector("[data-payment-link]");
  const copyButton = page.querySelector("[data-copy-activation]");
  let completed = false;

  const labels = {
    pending: "等待付款",
    paid: "已付款，正在发放",
    expired: "订单已过期",
    canceled: "订单已取消",
    failed: "处理失败",
  };

  const render = (payload) => {
    const paymentStatus = payload.status || "pending";
    const fulfillmentStatus = payload.fulfillment_status || "pending";
    if (state) {
      state.textContent = labels[paymentStatus] || paymentStatus;
      state.className = "seerr-badge payment-status-" + paymentStatus;
    }
    if (hint) {
      hint.textContent = paymentStatus === "pending" ? "请打开微信收银台完成付款。" :
        paymentStatus === "paid" && fulfillmentStatus !== "completed" ? "款项已确认，正在生成权益。" :
        paymentStatus === "expired" ? "订单已过期，请重新选择方案。" : "";
    }
    if (paymentLink) paymentLink.hidden = paymentStatus !== "pending";
    if (fulfillmentStatus === "completed") {
      completed = true;
      if (result) result.hidden = false;
      if (code && payload.activation_code) code.textContent = payload.activation_code;
    }
  };

  const poll = async () => {
    try {
      const response = await fetch("/payment/" + encodeURIComponent(token) + "/status", { cache: "no-store" });
      if (!response.ok) return;
      render(await response.json());
    } catch (_) {
      // The next poll can recover from a transient network failure.
    }
  };

  if (copyButton && code) {
    copyButton.addEventListener("click", async () => {
      const value = code.textContent.trim();
      if (!value) return;
      try {
        if (navigator.clipboard && window.isSecureContext) {
          await navigator.clipboard.writeText(value);
        } else {
          const input = document.createElement("textarea");
          input.value = value;
          input.style.position = "fixed";
          input.style.opacity = "0";
          document.body.appendChild(input);
          input.select();
          document.execCommand("copy");
          input.remove();
        }
        copyButton.textContent = "已复制";
        window.setTimeout(() => { copyButton.textContent = "复制激活码"; }, 1500);
      } catch (_) {
        copyButton.textContent = "请手动复制";
      }
    });
  }

  poll();
  const interval = window.setInterval(() => {
    if (!completed) poll();
    else window.clearInterval(interval);
  }, 2500);
})();

/* ============================================================
   Seerr 风格交互（公开页 + 用户中心）
   ============================================================ */

/* --- 顶栏滚动毛玻璃（seerr Layout 风格） --- */

(() => {
  const topbar = document.querySelector("[data-seerr-topbar]");
  if (!topbar) return;
  const update = () => topbar.classList.toggle("scrolled", window.scrollY > 20);
  update();
  window.addEventListener("scroll", update, { passive: true });
})();

/* --- 滚动入场动画（.reveal） --- */

(() => {
  const elements = document.querySelectorAll(".reveal");
  if (!elements.length) return;
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("visible");
            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.12, rootMargin: "0px 0px -32px 0px" }
    );
    elements.forEach((element) => observer.observe(element));
  } else {
    elements.forEach((element) => element.classList.add("visible"));
  }
})();

/* --- 顶部加载进度条（nprogress 风格） --- */

(() => {
  const progress = document.querySelector("[data-seerr-progress]");
  if (!progress) return;
  const fire = () => {
    progress.classList.remove("active");
    void progress.offsetWidth; // 强制重排以重新触发动画
    progress.classList.add("active");
  };
  if (document.readyState === "complete") {
    window.setTimeout(fire, 180);
  } else {
    window.addEventListener("load", () => window.setTimeout(fire, 180));
  }
  document.addEventListener("submit", (event) => {
    if (event.target.matches("[data-seerr-submit]")) fire();
  });
})();

/* ============================================================
   最近更新海报加载失败时显示首字占位
   （data-fallback-poster；捕获阶段监听，避免内联脚本被 CSP 拦截）
   ============================================================ */

document.addEventListener(
  "error",
  (event) => {
    const img = event.target;
    if (!(img instanceof HTMLImageElement) || !img.dataset.fallbackPoster) return;
    const wrapper = img.closest(".seerr-recent-poster");
    if (!wrapper) return;
    img.style.display = "none";
    if (wrapper.querySelector(".seerr-result-placeholder")) return;
    const placeholder = document.createElement("span");
    placeholder.className = "seerr-result-placeholder";
    placeholder.setAttribute("aria-hidden", "true");
    placeholder.textContent = (img.alt || "?").trim().charAt(0) || "?";
    wrapper.appendChild(placeholder);
  },
  true
);

/* ============================================================
   最近更新横向滑动
   - 按钮（data-scroll="prev|next"）按一行卡片步进
   - 指针拖拽滚动（data-scroll-row），触摸/鼠标均可
   ============================================================ */

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-scroll]");
  if (!button) return;
  const row = button.closest(".seerr-panel")?.querySelector("[data-scroll-row]");
  if (!row) return;
  const card = row.querySelector(".seerr-recent-card");
  const step = card ? card.offsetWidth + 16 : 320;
  row.scrollBy({ left: button.dataset.scroll === "prev" ? -step : step, behavior: "smooth" });
});

document.addEventListener("pointerdown", (event) => {
  if (event.pointerType !== "mouse") return; // 触摸交给浏览器原生滚动（touch-action: pan-x）
  const row = event.target.closest("[data-scroll-row]");
  if (!row || row.scrollWidth <= row.clientWidth) return;
  if (event.target.closest("a, button, input, select, textarea")) return;
  const startX = event.clientX;
  const startScroll = row.scrollLeft;
  let dragging = false;

  const move = (moveEvent) => {
    const delta = moveEvent.clientX - startX;
    if (!dragging && Math.abs(delta) > 5) {
      dragging = true;
      row.style.scrollSnapType = "none";
    }
    if (dragging) row.scrollLeft = startScroll - delta;
  };
  const up = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", up);
    document.removeEventListener("pointercancel", up);
    if (dragging) row.style.scrollSnapType = "";
  };
  document.addEventListener("pointermove", move);
  document.addEventListener("pointerup", up);
  document.addEventListener("pointercancel", up);
});
