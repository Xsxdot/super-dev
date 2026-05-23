export interface SearchHighlightPart {
  text: string
  match: boolean
}

export function splitSearchHighlight(message: string, query: string): SearchHighlightPart[] {
  const trimmedQuery = query.trim()
  if (!trimmedQuery) return [{ text: message, match: false }]

  const lowerMessage = message.toLocaleLowerCase()
  const lowerQuery = trimmedQuery.toLocaleLowerCase()
  const parts: SearchHighlightPart[] = []
  let cursor = 0
  let index = lowerMessage.indexOf(lowerQuery)

  while (index >= 0) {
    if (index > cursor) parts.push({ text: message.slice(cursor, index), match: false })
    parts.push({ text: message.slice(index, index + trimmedQuery.length), match: true })
    cursor = index + trimmedQuery.length
    index = lowerMessage.indexOf(lowerQuery, cursor)
  }

  if (cursor < message.length) parts.push({ text: message.slice(cursor), match: false })
  return parts
}
