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
