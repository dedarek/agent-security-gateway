import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { StoryCards, type Story } from './StoryCards'
import { LineageGraph } from './LineageGraph'
import { EvidenceCard } from './EvidenceCard'
import { Drawer } from './Drawer'
import { SkeletonRows } from './Skeleton'

type Layer = 'stories' | 'lineage'

/** Insight — the combined B/D/E ontology console.
 *  E (default): session story cards. Click → B focused lineage.
 *  B: taint-lineage subgraph. Click a BLOCK verdict node/edge → D evidence.
 *  D: engine-vote matrix drawer.
 */
export function OntoInsight() {
  const [params, setParams] = useSearchParams()
  const focusParam = params.get('focus') || ''

  const { data: storiesData, isLoading } = useQuery({ queryKey: ['onto-stories'], queryFn: api.ontoStories, refetchInterval: 12000 })
  const stories: Story[] = storiesData?.stories || []

  const [layer, setLayer] = useState<Layer>(focusParam ? 'lineage' : 'stories')
  const [focus, setFocus] = useState<string>(focusParam)
  const [evidenceFor, setEvidenceFor] = useState<string | null>(null)
  const [showGlobal, setShowGlobal] = useState(false)

  const openStory = (s: Story) => {
    setFocus('story:' + s.session_id)
    setLayer('lineage')
    setShowGlobal(false)
  }
  const backToStories = () => {
    setLayer('stories')
    setFocus('')
    setShowGlobal(false)
    setParams((p) => { p.delete('focus'); return p }, { replace: true })
  }

  // pick a verdict node → open its evidence (D layer)
  const onPickNode = (id: string, _props?: any) => {
    if (id.startsWith('verdict:')) setEvidenceFor(id.slice('verdict:'.length))
  }

  return (
    <div style={{ padding: 22, display: 'flex', flexDirection: 'column', gap: 14, minHeight: 0 }}>
      {/* layer breadcrumb */}
      <div className="row" style={{ gap: 8 }}>
        <button className={`seg-item ${layer === 'stories' ? 'on' : ''}`} onClick={backToStories}
          style={{ padding: '6px 14px', borderRadius: 6, border: '1px solid var(--line)', background: layer === 'stories' ? 'var(--bg-1)' : 'transparent', cursor: 'pointer', fontWeight: 600, fontSize: 12 }}>
          故事目录
        </button>
        <span className="dim">/</span>
        <span className={`seg-item ${layer === 'lineage' ? 'on' : ''}`} style={{ padding: '6px 14px', borderRadius: 6, border: '1px solid var(--line)', fontWeight: 600, fontSize: 12, background: layer === 'lineage' ? 'var(--bg-1)' : 'transparent', color: layer === 'lineage' ? 'var(--fg-0)' : 'var(--fg-2)' }}>
          血缘图 {focus && <span className="dim mono" style={{ fontWeight: 400 }}>{focus.replace(/^story:/, '').slice(0, 24)}</span>}
        </span>
        <span className="dim">/</span>
        <span className="small dim">点红节点看裁决证据</span>
        <div style={{ marginLeft: 'auto' }}>
          {layer === 'lineage' && (
            <div className="seg">
              <button className={`seg-item ${!showGlobal ? 'on' : ''}`} onClick={() => setShowGlobal(false)}>焦点</button>
              <button className={`seg-item ${showGlobal ? 'on' : ''}`} onClick={() => setShowGlobal(true)}>全局</button>
            </div>
          )}
        </div>
      </div>

      {layer === 'stories' && (
        <>
          <div className="small dim">每个会话一个故事。红色 = 被拦。点卡片进入它的数据血缘图。</div>
          {isLoading ? <SkeletonRows n={5} h={76} /> : <StoryCards stories={stories} onOpen={openStory} />}
        </>
      )}

      {layer === 'lineage' && (
        <div className="card card-pad">
          <LineageGraph
            focus={showGlobal ? undefined : focus || undefined}
            onPickNode={onPickNode}
          />
        </div>
      )}

      <Drawer open={!!evidenceFor} onClose={() => setEvidenceFor(null)} title="裁决证据 · 引擎为什么这样判">
        {evidenceFor && <EvidenceCard eventId={evidenceFor} />}
      </Drawer>
    </div>
  )
}
