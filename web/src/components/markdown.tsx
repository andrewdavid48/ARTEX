"use client";

import * as React from "react";

import ReactMarkdown, { type Components, defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";

// Inline raster images a model may embed directly. SVG is left out.
const DATA_IMAGE = /^data:image\/(png|jpe?g|gif|webp);base64,/i;

// urlTransform extends react-markdown's default (which drops data: URLs and would
// resolve a file path against the page URL):
// an image source that is a file path (absolute, relative or file://) is served from
// the workspace through the authenticated download endpoint, so an agent can show a
// screenshot it saved by writing ![alt](/path/to/file.png).
function urlTransform(url: string, key: string): string {
  if (key !== "src") return defaultUrlTransform(url);
  if (DATA_IMAGE.test(url)) return url;
  const path = url.replace(/^file:\/\//i, "");
  if (path && !/^[a-z][a-z0-9+.-]*:/i.test(path) && !path.startsWith("//")) {
    return `/api/workspace/download?path=${encodeURIComponent(path)}`;
  }
  return defaultUrlTransform(url);
}

// Img shows an image capped in height; clicking it toggles the uncapped size.
function Img({ node, src, alt, ...p }: React.ComponentProps<"img"> & { node?: unknown }) {
  const [full, setFull] = React.useState(false);
  if (!src) return alt ? <span className="text-muted-foreground">[{alt}]</span> : null;
  return (
    <button
      type="button"
      onClick={() => setFull((f) => !f)}
      className={`my-1.5 block max-w-full ${full ? "cursor-zoom-out" : "cursor-zoom-in"}`}
      aria-label={full ? "Shrink image" : "Enlarge image"}
    >
      {/* biome-ignore lint/performance/noImgElement: sources are data: URLs and an authenticated API route, which next/image cannot optimise */}
      <img
        {...p}
        src={src}
        alt={alt ?? ""}
        title={alt}
        loading="lazy"
        className={`max-w-full rounded-md border border-border ${full ? "" : "max-h-[480px]"}`}
      />
    </button>
  );
}

// Tailwind-styled element overrides (no typography plugin in this project, so we
// style each element). `node` is stripped — it's not a valid DOM attribute.
const components: Components = {
  h1: ({ node, ...p }) => <div className="mt-2.5 text-base font-semibold" {...p} />,
  h2: ({ node, ...p }) => <div className="mt-2 text-sm font-semibold" {...p} />,
  h3: ({ node, ...p }) => <div className="mt-1.5 text-sm font-medium text-foreground/90" {...p} />,
  h4: ({ node, ...p }) => <div className="mt-1.5 text-sm font-medium text-foreground/90" {...p} />,
  p: ({ node, ...p }) => <p className="leading-relaxed" {...p} />,
  ul: ({ node, ...p }) => <ul className="my-1 list-disc space-y-0.5 pl-5" {...p} />,
  ol: ({ node, ...p }) => <ol className="my-1 list-decimal space-y-0.5 pl-5" {...p} />,
  strong: ({ node, ...p }) => <strong className="font-semibold" {...p} />,
  a: ({ node, ...p }) => (
    <a className="text-primary underline underline-offset-2" target="_blank" rel="noreferrer" {...p} />
  ),
  hr: () => <hr className="my-2 border-border" />,
  blockquote: ({ node, ...p }) => (
    <blockquote className="my-1 border-l-2 border-border pl-2.5 text-muted-foreground" {...p} />
  ),
  pre: ({ node, ...p }) => (
    <pre className="my-1.5 overflow-auto rounded-md bg-muted/70 p-2.5 text-xs leading-relaxed" {...p} />
  ),
  code: ({ node, className, children, ...rest }) => {
    const block = /language-/.test(className || "");
    return block ? (
      <code className={"font-mono text-xs " + (className || "")} {...rest}>
        {children}
      </code>
    ) : (
      <code className="break-all rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]" {...rest}>
        {children}
      </code>
    );
  },
  table: ({ node, ...p }) => (
    <div className="my-1.5 overflow-x-auto">
      <table className="w-full border-collapse text-xs" {...p} />
    </div>
  ),
  th: ({ node, ...p }) => (
    <th className="border border-border bg-muted/60 px-2 py-1 text-left font-medium" {...p} />
  ),
  td: ({ node, ...p }) => <td className="border border-border px-2 py-1 align-top" {...p} />,
  img: Img,
};

export function Markdown({ text }: { text: string }) {
  return (
    <div className="min-w-0 space-y-1.5 text-sm [&>*:first-child]:mt-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} urlTransform={urlTransform}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
