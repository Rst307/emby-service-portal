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
