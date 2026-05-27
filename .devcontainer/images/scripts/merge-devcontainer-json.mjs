#!/usr/bin/env node
/*
 * Deep-merges a JSONC template with a JSON override.
 * Template wins on structure/refs (feature URLs, lifecycle commands);
 * override wins on user choices (enabled features, extensions, env).
 *
 * Usage: node merge-devcontainer-json.mjs <template.jsonc> <override.json> <output.json>
 */
import { readFileSync, writeFileSync } from "node:fs";

function stripJsonc(src) {
  let out = "";
  let i = 0;
  const n = src.length;
  let inString = false;
  let stringChar = "";
  while (i < n) {
    const c = src[i];
    const next = src[i + 1];
    if (inString) {
      out += c;
      if (c === "\\" && i + 1 < n) {
        out += src[i + 1];
        i += 2;
        continue;
      }
      if (c === stringChar) inString = false;
      i++;
      continue;
    }
    if (c === '"' || c === "'") {
      inString = true;
      stringChar = c;
      out += c;
      i++;
      continue;
    }
    if (c === "/" && next === "/") {
      while (i < n && src[i] !== "\n") i++;
      continue;
    }
    if (c === "/" && next === "*") {
      i += 2;
      while (i < n && !(src[i] === "*" && src[i + 1] === "/")) i++;
      i += 2;
      continue;
    }
    out += c;
    i++;
  }
  return out.replace(/,(\s*[\]}])/g, "$1");
}

function deepMerge(base, over) {
  if (over === undefined) return base;
  if (Array.isArray(over)) return over;
  if (over === null || typeof over !== "object") return over;
  if (base === null || typeof base !== "object" || Array.isArray(base)) {
    return { ...over };
  }
  const out = { ...base };
  for (const k of Object.keys(over)) {
    out[k] = deepMerge(base[k], over[k]);
  }
  return out;
}

const [, , tmplPath, overPath, outPath] = process.argv;
if (!tmplPath || !overPath || !outPath) {
  console.error(
    "usage: merge-devcontainer-json.mjs <template> <override> <output>",
  );
  process.exit(2);
}

const tmpl = JSON.parse(stripJsonc(readFileSync(tmplPath, "utf8")));
const over = JSON.parse(readFileSync(overPath, "utf8"));
const merged = deepMerge(tmpl, over);

writeFileSync(outPath, JSON.stringify(merged, null, 2) + "\n");
