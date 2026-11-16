import { Runners } from "./actions/Runners.tsx";

/** Admin: runners available to every repository of the instance. */
export function AdminRunners() {
  return <Runners scope={{ kind: "instance" }} />;
}
