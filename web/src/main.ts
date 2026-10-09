import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/600.css";
import "@fontsource/barlow-condensed/500.css";
import "@fontsource/barlow-condensed/600.css";
import "@fontsource/barlow-condensed/700.css";
import "./styles.css";
import { App } from "./app";

const root = document.getElementById("app");
if (root) {
  const app = new App(root);
  app.start();
  (window as unknown as { pitwall?: App }).pitwall = app;
}
