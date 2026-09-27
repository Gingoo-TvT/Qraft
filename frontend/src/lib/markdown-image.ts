// Render imported image syntax as React attributes, never as raw HTML.
export function safeImageSource(source: string): string | null {
  const value = source.trim();
  if (!value || /[\u0000-\u001f\u007f\\]/.test(value)) return null;
  if (/^https?:\/\//i.test(value)) return value;
  if (/^\/\//.test(value)) return 'https:' + value;
  if (/^data:image\/(?:png|jpeg|gif|webp);base64,[a-z0-9+/=]+$/i.test(value)) return value;
  if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return null;
  return value;
}

function decodeAttribute(value: string): string {
  return value.replace(/&(?:amp|quot|apos|lt|gt|#\d+|#x[\da-f]+);/gi, entity => {
    const names: Record<string, string> = { '&amp;': '&', '&quot;': '"', '&apos;': "'", '&lt;': '<', '&gt;': '>' };
    if (entity.toLowerCase() in names) return names[entity.toLowerCase()];
    const hex = /^&#x/i.test(entity);
    const code = Number.parseInt(entity.slice(hex ? 3 : 2, -1), hex ? 16 : 10);
    return code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : '';
  });
}

export function markdownImage(token: string): { src: string; alt: string; title?: string } | null {
  let source = '', alt = '', title: string | undefined;
  if (/^<img\b/i.test(token)) {
    const attributes = /(?:\s)(src|alt|title)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/gi;
    for (const match of token.matchAll(attributes)) {
      const value = decodeAttribute(match[2] ?? match[3] ?? match[4]);
      if (match[1].toLowerCase() === 'src') source = value;
      if (match[1].toLowerCase() === 'alt') alt = value;
      if (match[1].toLowerCase() === 'title') title = value;
    }
  } else {
    const image = /^!\[((?:\\.|[^\]\\])*)\]\(\s*([\s\S]*?)\s*\)$/.exec(token);
    if (!image) return null;
    const destination = /^(?:<([^>]+)>|(\S+?))(?:\s+(?:"([^"]*)"|'([^']*)'))?$/.exec(image[2]);
    if (!destination) return null;
    source = (destination[1] ?? destination[2]).replace(/\\([()\\])/g, '$1');
    alt = image[1].replace(/\\([\[\]\\])/g, '$1');
    title = destination[3] ?? destination[4];
  }
  const src = safeImageSource(source);
  return src ? { src, alt, title } : null;
}
