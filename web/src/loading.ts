import topbar from "topbar";

let activeLoads = 0;
let delayedShow: number | undefined;

function configureTopbar() {
  const accent = getComputedStyle(document.documentElement)
    .getPropertyValue("--accent")
    .trim();
  const color = accent || "#74172f";
  topbar.config({
    autoRun: false,
    barThickness: 2,
    barColors: { "0": color, "1.0": color },
    shadowBlur: 0,
  });
}

export function beginLoading(): () => void {
  activeLoads += 1;
  if (activeLoads === 1) {
    configureTopbar();
    delayedShow = window.setTimeout(() => {
      delayedShow = undefined;
      topbar.show();
    }, 200);
  }

  let finished = false;
  return () => {
    if (finished) return;
    finished = true;
    activeLoads = Math.max(0, activeLoads - 1);
    if (activeLoads !== 0) return;
    if (delayedShow !== undefined) {
      window.clearTimeout(delayedShow);
      delayedShow = undefined;
    }
    topbar.hide();
  };
}
