// Decision Stack — Animated Microphone Button
// Press/hold to record, tap to toggle voice mode

import React, { useEffect, useRef } from 'react';
import {
  View,
  TouchableOpacity,
  StyleSheet,
  Animated,
} from 'react-native';
import { palette } from '@theme/colors';
import { spacing } from '@theme/spacing';

interface VoiceInputButtonProps {
  isRecording: boolean;
  onPress: () => void;
  disabled?: boolean;
}

/**
 * VoiceInputButton — animated mic button with recording state feedback.
 * Idle: subtle sand icon.
 * Recording: pulsing red ring to indicate active recording.
 */
export const VoiceInputButton: React.FC<VoiceInputButtonProps> = ({
  isRecording,
  onPress,
  disabled = false,
}) => {
  const pulseAnim = useRef(new Animated.Value(1)).current;
  const scaleAnim = useRef(new Animated.Value(1)).current;

  // Pulse animation when recording
  useEffect(() => {
    if (isRecording) {
      // Breathing pulse effect
      Animated.loop(
        Animated.sequence([
          Animated.timing(pulseAnim, {
            toValue: 1.35,
            duration: 800,
            useNativeDriver: true,
          }),
          Animated.timing(pulseAnim, {
            toValue: 1,
            duration: 800,
            useNativeDriver: true,
          }),
        ])
      ).start();

      // Subtle scale bounce
      Animated.loop(
        Animated.sequence([
          Animated.timing(scaleAnim, {
            toValue: 1.05,
            duration: 600,
            useNativeDriver: true,
          }),
          Animated.timing(scaleAnim, {
            toValue: 1,
            duration: 600,
            useNativeDriver: true,
          }),
        ])
      ).start();
    } else {
      // Reset animations
      pulseAnim.setValue(1);
      scaleAnim.setValue(1);
    }
  }, [isRecording, pulseAnim, scaleAnim]);

  return (
    <View style={styles.container}>
      {/* Recording pulse ring */}
      {isRecording && (
        <Animated.View
          style={[
            styles.pulseRing,
            {
              transform: [{ scale: pulseAnim }],
              opacity: pulseAnim.interpolate({
                inputRange: [1, 1.35],
                outputRange: [0.5, 0],
              }),
            },
          ]}
        />
      )}

      <TouchableOpacity
        onPress={onPress}
        disabled={disabled}
        activeOpacity={0.7}
        style={[
          styles.button,
          isRecording ? styles.buttonRecording : styles.buttonIdle,
          disabled && styles.buttonDisabled,
        ]}
        accessibilityLabel={isRecording ? 'Stop recording' : 'Start voice input'}
        accessibilityRole="button"
      >
        <Animated.View style={{ transform: [{ scale: scaleAnim }] }}>
          {/* Mic icon as SVG-like shapes */}
          <View style={styles.micContainer}>
            <View
              style={[
                styles.micHead,
                isRecording
                  ? { backgroundColor: '#ffffff' }
                  : { backgroundColor: palette.sand[400] },
              ]}
            />
            <View
              style={[
                styles.micStem,
                isRecording
                  ? { backgroundColor: '#ffffff' }
                  : { backgroundColor: palette.sand[400] },
              ]}
            />
            <View
              style={[
                styles.micBase,
                isRecording
                  ? { borderColor: '#ffffff' }
                  : { borderColor: palette.sand[400] },
              ]}
            />
          </View>
        </Animated.View>
      </TouchableOpacity>
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    alignItems: 'center',
    justifyContent: 'center',
  },
  pulseRing: {
    position: 'absolute',
    width: spacing[14],
    height: spacing[14],
    borderRadius: spacing[7],
    backgroundColor: palette.rose[400],
  },
  button: {
    width: spacing[11],
    height: spacing[11],
    borderRadius: spacing[5.5],
    justifyContent: 'center',
    alignItems: 'center',
    zIndex: 2,
  },
  buttonIdle: {
    backgroundColor: palette.sand[50],
    borderWidth: 1.5,
    borderColor: palette.sand[300],
  },
  buttonRecording: {
    backgroundColor: palette.rose[500],
    borderWidth: 0,
  },
  buttonDisabled: {
    opacity: 0.5,
  },
  micContainer: {
    alignItems: 'center',
    justifyContent: 'center',
  },
  micHead: {
    width: spacing[3.5],
    height: spacing[4.5],
    borderRadius: spacing[2],
    marginBottom: 1,
  },
  micStem: {
    width: 2,
    height: spacing[2],
  },
  micBase: {
    width: spacing[3.5],
    height: spacing[1.5],
    borderBottomWidth: 2,
    borderLeftWidth: 2,
    borderRightWidth: 2,
    borderBottomLeftRadius: spacing[1.5],
    borderBottomRightRadius: spacing[1.5],
    marginTop: -1,
  },
});
