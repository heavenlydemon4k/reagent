// Decision Stack — Live Transcription Display
// Shows real-time STT text as the user speaks, with confidence coloring

import React, { useEffect, useRef } from 'react';
import {
  View,
  Text,
  ScrollView,
  StyleSheet,
  Animated,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight, lineHeight } from '@theme/typography';
import { spacing } from '@theme/spacing';

interface TranscriptionViewProps {
  text: string;
  isFinal: boolean;
  isActive: boolean;
  confidence?: number; // 0.0 - 1.0
}

/**
 * TranscriptionView — live display of speech-to-transcription text.
 * Shows interim (partial) text in muted color and pulses while listening.
 * Final text appears in full color.
 */
export const TranscriptionView: React.FC<TranscriptionViewProps> = ({
  text,
  isFinal,
  isActive,
  confidence = 0.95,
}) => {
  const fadeAnim = useRef(new Animated.Value(0)).current;
  const pulseAnim = useRef(new Animated.Value(1)).current;
  const scrollViewRef = useRef<ScrollView>(null);

  // Fade in on mount
  useEffect(() => {
    Animated.timing(fadeAnim, {
      toValue: 1,
      duration: 300,
      useNativeDriver: true,
    }).start();
  }, [fadeAnim]);

  // Pulsing cursor animation while active
  useEffect(() => {
    if (isActive && !isFinal) {
      Animated.loop(
        Animated.sequence([
          Animated.timing(pulseAnim, {
            toValue: 0.4,
            duration: 600,
            useNativeDriver: true,
          }),
          Animated.timing(pulseAnim, {
            toValue: 1,
            duration: 600,
            useNativeDriver: true,
          }),
        ])
      ).start();
    } else {
      pulseAnim.setValue(1);
    }
  }, [isActive, isFinal, pulseAnim]);

  // Auto-scroll to bottom as text grows
  useEffect(() => {
    if (text && scrollViewRef.current) {
      setTimeout(() => {
        scrollViewRef.current?.scrollToEnd({ animated: true });
      }, 50);
    }
  }, [text]);

  const hasText = text.length > 0;

  return (
    <Animated.View
      style={[
        styles.container,
        { opacity: fadeAnim },
        isActive && !isFinal && styles.containerActive,
      ]}
    >
      {/* Status label */}
      <View style={styles.statusRow}>
        <Animated.View
          style={[
            styles.statusDot,
            {
              opacity: pulseAnim,
              backgroundColor: isActive
                ? isFinal
                  ? palette.sage[500]
                  : palette.sand[400]
                : palette.ink[300],
            },
          ]}
        />
        <Text style={styles.statusText}>
          {isActive
            ? isFinal
              ? 'Transcribed'
              : 'Listening…'
            : 'Ready'}
        </Text>

        {/* Confidence indicator */}
        {hasText && (
          <Text style={styles.confidenceText}>
            {Math.round(confidence * 100)}%
          </Text>
        )}
      </View>

      {/* Transcription text */}
      <ScrollView
        ref={scrollViewRef}
        style={styles.scrollView}
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {hasText ? (
          <Text
            style={[
              styles.transcriptionText,
              !isFinal && styles.transcriptionInterim,
            ]}
          >
            {text}
            {!isFinal && (
              <Animated.Text
                style={[
                  styles.cursor,
                  { opacity: pulseAnim },
                ]}
              >
                ▎
              </Animated.Text>
            )}
          </Text>
        ) : (
          <Text style={styles.placeholderText}>
            {isActive
              ? 'Speak now…'
              : 'Tap the microphone to start speaking'}
          </Text>
        )}
      </ScrollView>
    </Animated.View>
  );
};

const styles = StyleSheet.create({
  container: {
    backgroundColor: '#ffffff',
    borderRadius: spacing[3],
    borderWidth: 1,
    borderColor: palette.ink[100],
    padding: spacing[4],
    minHeight: spacing[20],
  },
  containerActive: {
    borderColor: palette.sand[300],
    backgroundColor: palette.sand[50],
  },
  statusRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: spacing[3],
  },
  statusDot: {
    width: spacing[2],
    height: spacing[2],
    borderRadius: spacing[1],
    marginRight: spacing[2],
  },
  statusText: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.medium,
    color: palette.ink[500],
    flex: 1,
  },
  confidenceText: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.semibold,
    color: palette.sage[600],
  },
  scrollView: {
    maxHeight: spacing[32],
  },
  scrollContent: {
    flexGrow: 1,
  },
  transcriptionText: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.normal,
    color: palette.ink[900],
    lineHeight: lineHeight.relaxed * fontSize.lg,
  },
  transcriptionInterim: {
    color: palette.ink[600],
  },
  cursor: {
    color: palette.sand[400],
    fontWeight: fontWeight.normal,
  },
  placeholderText: {
    fontSize: fontSize.base,
    color: palette.ink[400],
    fontStyle: 'italic',
    textAlign: 'center',
    paddingVertical: spacing[4],
  },
});
