import { ref } from "vue";

export const currentPath = ref(window.location.pathname);

window.addEventListener("popstate", () => {
  currentPath.value = window.location.pathname;
});

export function navigate(path: string) {
  window.history.pushState({}, "", path);
  currentPath.value = path;
}
