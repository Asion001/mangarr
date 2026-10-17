/** scrollToTop brings el to the top of the box that scrolls it, and only that
 *  box. scrollIntoView also scrolls every ancestor that can move, overflow
 *  hidden or not, and in an iOS home screen app that slid the whole layout up
 *  under the status bar. */
export function scrollToTop(el: HTMLElement) {
  for (let box = el.parentElement; box; box = box.parentElement) {
    if (box.scrollHeight <= box.clientHeight || !/auto|scroll/.test(getComputedStyle(box).overflowY)) continue;
    box.scrollTop += el.getBoundingClientRect().top - box.getBoundingClientRect().top;
    return;
  }
}
