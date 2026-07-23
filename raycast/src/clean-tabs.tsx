import {
  Alert,
  closeMainWindow,
  confirmAlert,
  showToast,
  Toast,
} from "@raycast/api";
import { cleanTabs } from "./bridge";

export default async function CleanTabs() {
  if (
    !(await confirmAlert({
      title: "Sweep ungrouped Chrome tabs?",
      message:
        "Chrome tabs not in any tab group will be moved into a reserved native group (not closed).",
      primaryAction: { title: "Sweep" },
      dismissAction: { title: "Cancel" },
    }))
  ) {
    return;
  }

  try {
    const res = await cleanTabs();
    const moved = res?.moved;
    await showToast({
      style: Toast.Style.Success,
      title: "Swept",
      message:
        moved != null
          ? moved === 0
            ? "No ungrouped tabs"
            : `${moved} tab${moved !== 1 ? "s" : ""} moved to ${res?.group ?? "reserved group"}`
          : "Done",
    });
    await closeMainWindow();
  } catch (e) {
    await showToast({
      style: Toast.Style.Failure,
      title: "Failed",
      message: String(e),
    });
  }
}
