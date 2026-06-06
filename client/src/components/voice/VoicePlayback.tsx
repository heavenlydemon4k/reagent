// Decision Stack — TTS Audio Playback Control
// Speaker icon + inline audio player for assistant TTS responses

import React from 'react';
import {
  View,
  TouchableOpacity,
  Text,
  StyleSheet,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';

interface VoicePlaybackProps {
  audioUrl: string;
  isPlaying: boolean;
  onPlay: () => void;
}

/**
 * VoicePlayback — small inline audio player attached to assistant messages.
 * Shows a speaker icon and tap-to-play TTS audio.
 */
export const VoicePlayback: React.FC<VoicePlaybackProps> = ({
  audioUrl,
  isPlaying,
  onPlay,
}) => {
  if (!audioUrl) return null;

  return (
    <TouchableOpacity
      onPress={onPlay}
      style={[styles.container, isPlaying && styles.containerPlaying]}
      activeOpacity={0.7}
      accessibilityLabel={isPlaying ? 'Playing audio' : 'Play audio response'}
      accessibilityRole="button"
    >
      {/* Speaker icon */}
      <View style={styles.iconContainer}>
        <View style={styles.speakerIcon}>
          <View style={styles.speakerBody} />
          <View style={styles.speakerCone} />
          {isPlaying && (
            <>
              <View style={[styles.soundWave, styles.soundWave1]} />
              <View style={[styles.soundWave, styles.soundWave2]} />
            </>
          )}
        </View>
      </View>

      <Text style={[styles.label, isPlaying && styles.labelPlaying]}>
        {isPlaying ? 'Playing…' : 'Listen'}
      </Text>
    </TouchableOpacity>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: spacing[2.5],
    paddingVertical: spacing[1.5],
    paddingHorizontal: spacing[3],
    backgroundColor: palette.sand[50],
    borderRadius: spacing[2.5],
    alignSelf: 'flex-start',
    borderWidth: 1,
    borderColor: palette.sand[200],
  },
  containerPlaying: {
    backgroundColor: palette.sand[100],
    borderColor: palette.sand[300],
  },
  iconContainer: {
    marginRight: spacing[2],
  },
  speakerIcon: {
    width: spacing[4],
    height: spacing[4],
    justifyContent: 'center',
    alignItems: 'center',
    flexDirection: 'row',
  },
  speakerBody: {
    width: spacing[1.5],
    height: spacing[2.5],
    backgroundColor: palette.sand[600],
    borderRadius: 1,
  },
  speakerCone: {
    width: 0,
    height: 0,
    backgroundColor: 'transparent',
    borderStyle: 'solid',
    borderLeftWidth: 6,
    borderTopWidth: 5,
    borderBottomWidth: 5,
    borderLeftColor: palette.sand[600],
    borderTopColor: 'transparent',
    borderBottomColor: 'transparent',
    marginLeft: -1,
  },
  soundWave: {
    position: 'absolute',
    right: -4,
    width: 3,
    borderRadius: 1.5,
    backgroundColor: palette.sand[400],
  },
  soundWave1: {
    height: 6,
    top: 4,
  },
  soundWave2: {
    height: 10,
    top: 2,
    right: -7,
  },
  label: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.medium,
    color: palette.sand[700],
  },
  labelPlaying: {
    color: palette.sand[800],
    fontWeight: fontWeight.semibold,
  },
});
