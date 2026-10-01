// MV3 service worker. 툴바 아이콘 클릭 시 side panel 열기.
export default defineBackground(() => {
  browser.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch(console.error);
});
