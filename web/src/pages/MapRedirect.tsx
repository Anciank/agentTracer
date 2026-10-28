import { useEffect } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

/** Keep /map/:id working → project page map tab. */
export default function MapRedirect() {
  const { projectId = "" } = useParams();
  const [sp] = useSearchParams();
  const nav = useNavigate();
  const win = sp.get("window") || "24h";
  const rare = sp.get("rare");
  const expand = sp.get("expand");
  const qs = new URLSearchParams({ tab: "map", window: win });
  if (rare) qs.set("rare", rare);
  if (expand) qs.set("expand", expand);
  const to = `/projects/${encodeURIComponent(projectId)}?${qs.toString()}`;
  useEffect(() => {
    nav(to, { replace: true });
  }, [nav, to]);
  return <div className="page empty">Opening map…</div>;
}
