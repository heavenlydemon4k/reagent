import React from "react";
import { View, Text, TouchableOpacity } from "react-native";
import { cardStyles } from "../../styles/cardStyles";
import type { ChunkCitation } from "../../types/cards";

interface CitationChipProps {
  citations: ChunkCitation[];
  onPressCitation: (citation: ChunkCitation) => void;
}

/**
 * CitationChip — clickable chunk_id chips showing citation count
 * Renders as a row of chips, each showing a short chunk_id reference.
 */
export const CitationChip: React.FC<CitationChipProps> = ({
  citations,
  onPressCitation,
}) => {
  if (!citations || citations.length === 0) return null;

  return (
    <View style={cardStyles.citationRow}>
      {citations.map((c, idx) => (
        <TouchableOpacity
          key={c.chunk_id}
          style={cardStyles.citationChip}
          onPress={() => onPressCitation(c)}
          activeOpacity={0.7}
        >
          <Text style={cardStyles.citationChipText}>
            {`#${idx + 1} ${c.chunk_id.slice(0, 8)}`}
          </Text>
        </TouchableOpacity>
      ))}
    </View>
  );
};
