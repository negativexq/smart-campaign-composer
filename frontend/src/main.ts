import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import "./styles.css";

import { createApp } from "vue";

import App from "./App.vue";
import { startTheme } from "./lib/preferences";

startTheme();
createApp(App).mount("#app");
