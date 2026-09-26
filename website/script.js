// Screenshot tabs in the "Web admin" section.
const shot = document.getElementById("shot");
document.querySelectorAll("[data-shot]").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll("[data-shot]").forEach((b) => b.classList.toggle("on", b === btn));
    shot.style.opacity = "0";
    setTimeout(() => {
      shot.src = `assets/${btn.dataset.shot}.png`;
      shot.onload = () => (shot.style.opacity = "1");
    }, 150);
  });
});

// Install tabs.
document.querySelectorAll("[data-pane]").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll("[data-pane]").forEach((b) => b.classList.toggle("on", b === btn));
    document.querySelectorAll(".pane").forEach((p) => p.classList.toggle("on", p.id === `pane-${btn.dataset.pane}`));
  });
});

// Copy buttons.
document.querySelectorAll(".copy").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const text = btn.parentElement.querySelector("code").innerText;
    try {
      await navigator.clipboard.writeText(text);
      btn.textContent = "Copied ✓";
    } catch {
      btn.textContent = "Select & copy";
    }
    setTimeout(() => (btn.textContent = "Copy"), 1800);
  });
});
