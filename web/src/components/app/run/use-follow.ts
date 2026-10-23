"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

// useFollow keeps the page scrolled to the bottom while new content arrives, until the reader scrolls up;
// `away` says they did (show a "jump to latest" control), jump() resumes following.
export function useFollow(dep: unknown, enabled: boolean) {
  const [away, setAway] = useState(false);
  const follow = useRef(true);
  const programmatic = useRef(false);

  useEffect(() => {
    const onScroll = () => {
      if (programmatic.current) return;
      const gap = document.documentElement.scrollHeight - window.scrollY - window.innerHeight;
      follow.current = gap < 96;
      setAway(!follow.current);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  const toBottom = useCallback((smooth: boolean) => {
    programmatic.current = true;
    window.scrollTo({ top: document.documentElement.scrollHeight, behavior: smooth ? "smooth" : "auto" });
    // Smooth scrolling fires scroll events on the way; ignore them until it settles.
    setTimeout(() => { programmatic.current = false; }, smooth ? 600 : 50);
  }, []);

  useLayoutEffect(() => {
    if (enabled && follow.current) toBottom(false);
  }, [dep, enabled, toBottom]);

  const jump = useCallback(() => {
    follow.current = true;
    setAway(false);
    toBottom(true);
  }, [toBottom]);

  return { away: enabled && away, jump };
}
