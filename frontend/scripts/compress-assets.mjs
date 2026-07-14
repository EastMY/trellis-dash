import { readdir, readFile, writeFile } from "node:fs/promises";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { constants, brotliCompress, gzip } from "node:zlib";
import { promisify } from "node:util";

const brotli = promisify(brotliCompress);
const gzipAsync = promisify(gzip);
const dist = fileURLToPath(new URL("../dist/", import.meta.url));
const compressible = new Set([".css", ".html", ".js", ".json", ".svg", ".txt", ".xml"]);

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesUnder(path));
    else files.push(path);
  }
  return files;
}

for (const path of await filesUnder(dist)) {
  if (!compressible.has(extname(path))) continue;
  const content = await readFile(path);
  // 极小文件压缩收益有限，保留原始表示即可。
  if (content.byteLength < 1024) continue;
  const [br, gz] = await Promise.all([
    brotli(content, { params: { [constants.BROTLI_PARAM_QUALITY]: 9 } }),
    gzipAsync(content, { level: 9 }),
  ]);
  await Promise.all([
    writeFile(`${path}.br`, br),
    writeFile(`${path}.gz`, gz),
  ]);
}
