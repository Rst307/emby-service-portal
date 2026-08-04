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
  const selection = form.querySelector("[data-batch-selection]");
  const selected = () => [...document.querySelectorAll(".batch-select")];

  const updateOperationFields = () => {
    const isDuration = action.value === "extend" || action.value === "reduce";
    const isExpiry = action.value === "set_expiry";
    durationFields.forEach((field) => { field.hidden = !isDuration; });
    expiryField.hidden = !isExpiry;
    durationInput.required = isDuration;
    expiryInput.required = isExpiry;
  };
  const updateSelection = () => {
    const checkboxes = selected();
    const count = checkboxes.filter((checkbox) => checkbox.checked).length;
    selection.textContent = count ? `已选择 ${count} 个账号` : "尚未选择账号";
    if (selectAll) {
      selectAll.checked = checkboxes.length > 0 && count === checkboxes.length;
      selectAll.indeterminate = count > 0 && count < checkboxes.length;
    }
  };

  action.addEventListener("change", updateOperationFields);
  selected().forEach((checkbox) => checkbox.addEventListener("change", updateSelection));
  if (selectAll) {
    selectAll.addEventListener("change", () => {
      selected().forEach((checkbox) => { checkbox.checked = selectAll.checked; });
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
  updateSelection();
});
