import { useState, useEffect } from 'react'

interface Profile {
  agent_name: string
  agent_tone: string
  system_prompt_suffix: string
  preferences_json: object
}

interface Props {
  open: boolean
  onClose: () => void
  onSave: (p: Profile) => void
  profile: Profile | null
}

export default function ProfileDrawer({ open, onClose, onSave, profile }: Props) {
  const [form, setForm] = useState<Profile>({
    agent_name: 'Reagent',
    agent_tone: 'professional',
    system_prompt_suffix: '',
    preferences_json: {}
  })

  useEffect(() => {
    if (profile) setForm(profile)
  }, [profile])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-80 bg-slate-850 h-full shadow-xl p-6 flex flex-col">
        <h2 className="text-lg font-semibold mb-4">Profile & Personalization</h2>
        <div className="space-y-4 flex-1">
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Agent Name</label>
            <input
              value={form.agent_name}
              onChange={e => setForm({ ...form, agent_name: e.target.value })}
              className="w-full bg-slate-800 rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">Tone</label>
            <select
              value={form.agent_tone}
              onChange={e => setForm({ ...form, agent_tone: e.target.value })}
              className="w-full bg-slate-800 rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="professional">Professional</option>
              <option value="casual">Casual</option>
              <option value="witty">Witty</option>
              <option value="concise">Concise</option>
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1">System Prompt Suffix</label>
            <textarea
              value={form.system_prompt_suffix}
              onChange={e => setForm({ ...form, system_prompt_suffix: e.target.value })}
              rows={4}
              className="w-full bg-slate-800 rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="Additional instructions for the agent..."
            />
          </div>
        </div>
        <div className="flex gap-2 mt-6">
          <button onClick={onClose} className="flex-1 px-4 py-2 rounded bg-slate-700 hover:bg-slate-600 text-sm">Cancel</button>
          <button onClick={() => onSave(form)} className="flex-1 px-4 py-2 rounded bg-blue-600 hover:bg-blue-500 text-sm text-white">Save</button>
        </div>
      </div>
    </div>
  )
}
