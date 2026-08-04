document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-copy]");
  if (!button) return;
  const code = button.dataset.copy;
  try {
    await navigator.clipboard.writeText(code);
  } catch (_) {
    const input = document.createElement("textarea");
    input.value = code;
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    document.execCommand("copy");
    input.remove();
  }
  const original = button.textContent;
  button.textContent = "已复制";
  setTimeout(() => { button.textContent = original; }, 1500);
});

document.addEventListener("DOMContentLoaded", () => {
  const form = document.querySelector("#account-batch");
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
  const rows = () => [...document.querySelectorAll("[data-account-row]")];
  const selected = () => [...document.querySelectorAll(".batch-select")];
  const visible = () => selected().filter((checkbox) => {
    const row = checkbox.closest("[data-account-row]");
    return !row.hidden;
  });

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
    rows().forEach((row) => {
      row.hidden = filter !== "all" && row.dataset.accountStatus !== filter;
    });
  };
  const updateSelection = () => {
    const checkboxes = selected();
    const visibleCheckboxes = visible();
    const count = checkboxes.filter((checkbox) => checkbox.checked).length;
    const visibleCount = visibleCheckboxes.filter((checkbox) => checkbox.checked).length;
    selection.textContent = count ? `已选择 ${count} 个账号` : "尚未选择账号";
    if (selectAll) {
      selectAll.checked = visibleCheckboxes.length > 0 && visibleCount === visibleCheckboxes.length;
      selectAll.indeterminate = visibleCount > 0 && visibleCount < visibleCheckboxes.length;
    }
  };

  action.addEventListener("change", updateOperationFields);
  if (statusFilter) {
    statusFilter.addEventListener("change", () => {
      updateVisibility();
      updateSelection();
    });
  }
  selected().forEach((checkbox) => checkbox.addEventListener("change", updateSelection));
  if (selectAll) {
    selectAll.addEventListener("change", () => {
      visible().forEach((checkbox) => { checkbox.checked = selectAll.checked; });
      updateSelection();
    });
  }
  if (selectStatus) {
    selectStatus.addEventListener("click", () => {
      visible().forEach((checkbox) => { checkbox.checked = true; });
      updateSelection();
    });
  }
  if (clearSelection) {
    clearSelection.addEventListener("click", () => {
      selected().forEach((checkbox) => { checkbox.checked = false; });
      updateSelection();
    });
  }
  form.addEventListener("submit", (event) => {
    const count = selected().filter((checkbox) => checkbox.checked).length;
    if (!count) {
      event.preventDefault();
      alert("请至少选择一个账号");
      return;
    }
    if (!window.confirm(`确认对 ${count} 个账号执行此操作？`)) event.preventDefault();
  });
  updateOperationFields();
  updateVisibility();
  updateSelection();
});
