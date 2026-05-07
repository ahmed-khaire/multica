"use client";

import { lazy } from "react";

const LazyEmojiPicker = lazy(() =>
  import("./emoji-picker").then((m) => ({ default: m.EmojiPicker })),
);

export { LazyEmojiPicker };
