// Tenant-scoped media thumbnail. The media bytes endpoint requires the
// X-Tenant-ID header, which a plain <img> cannot send — so we fetch as a
// blob and hand out a short-lived object URL.
import { useEffect, useState } from "react";
import { getTenantId } from "../api";

export function mediaObjectUrl(id: string): Promise<string> {
  return fetch(`/v1/media/${id}/raw`, {
    headers: { "X-Tenant-ID": getTenantId() },
  }).then((r) => {
    if (!r.ok) throw new Error(`${r.status}`);
    return r.blob().then((b) => URL.createObjectURL(b));
  });
}

export default function MediaThumb({ id, width = 72, height = 40 }: {
  id: string; width?: number; height?: number;
}) {
  const [url, setUrl] = useState("");
  useEffect(() => {
    let objectUrl = "";
    let dead = false;
    mediaObjectUrl(id).then((u) => {
      if (dead) { URL.revokeObjectURL(u); return; }
      objectUrl = u;
      setUrl(u);
    }).catch(() => {});
    return () => { dead = true; if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [id]);
  if (!url) {
    return <div style={{ width, height, background: "#eee", borderRadius: 4 }} />;
  }
  return <img src={url} alt="" style={{ width, height, objectFit: "cover", borderRadius: 4 }} />;
}
