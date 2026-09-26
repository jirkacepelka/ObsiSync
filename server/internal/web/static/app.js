// Small behaviours of the web admin, kept out of the HTML so the
// Content-Security-Policy can forbid inline scripts.
document.addEventListener("change", (e) => {
  if (e.target.matches("select[data-autosubmit]")) e.target.form.submit();
});
document.addEventListener("submit", (e) => {
  const msg = e.target.dataset.confirm;
  if (msg && !confirm(msg)) e.preventDefault();
});
