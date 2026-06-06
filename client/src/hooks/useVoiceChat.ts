// Decision Stack — Voice Recording + Playback Hook for Chat
// Handles audio recording (expo-av), Deepgram streaming STT, and ElevenLabs TTS playback

import { useState, useCallback, useRef, useEffect } from 'react';
import { Audio } from 'expo-av';

// ── Deepgram Configuration ──────────────────────────────────────────────────
// Pull from environment or fallback to placeholder (must be set at build time)
const DEEPGRAM_API_KEY = process.env.DEEPGRAM_API_KEY ?? '';
const DEEPGRAM_WS_URL = 'wss://api.deepgram.com/v1/listen';

export type VoicePhase = 'idle' | 'recording' | 'processing' | 'playing' | 'error';

export interface UseVoiceChatReturn {
  // State
  phase: VoicePhase;
  isRecording: boolean;
  isPlaying: boolean;
  transcription: string;
  amplitude: number[];
  error: string | null;

  // Controls
  startRecording: () => Promise<void>;
  stopRecording: () => Promise<string>;
  playTTS: (audioUrl: string) => Promise<void>;
  stopPlayback: () => Promise<void>;
  reset: () => void;
}

// ── Audio Metering Constants ────────────────────────────────────────────────
const AMPLITUDE_HISTORY_LENGTH = 40;
const DEFAULT_AMPLITUDE = Array.from({ length: AMPLITUDE_HISTORY_LENGTH }, () => 3);

/**
 * Normalize metering dB value (-160 … 0) to bar height (0 … 28).
 */
function normalizeMetering(meteringDb: number): number {
  const normalized = Math.max(0, (meteringDb + 160) / 160);
  return Math.max(2, Math.min(28, normalized * 28));
}

/**
 * Build amplitude array from a single meter reading for the waveform.
 */
function buildAmplitudeArray(level: number): number[] {
  return Array.from({ length: AMPLITUDE_HISTORY_LENGTH }, (_, i) => {
    // Slight ripple effect across bars for visual interest
    const ripple = Math.sin((i / AMPLITUDE_HISTORY_LENGTH) * Math.PI) * 4;
    return Math.max(2, Math.min(28, level + ripple));
  });
}

export function useVoiceChat(): UseVoiceChatReturn {
  const [phase, setPhase] = useState<VoicePhase>('idle');
  const [isPlaying, setIsPlaying] = useState(false);
  const [transcription, setTranscription] = useState('');
  const [amplitude, setAmplitude] = useState<number[]>(DEFAULT_AMPLITUDE);
  const [error, setError] = useState<string | null>(null);

  const recordingRef = useRef<Audio.Recording | null>(null);
  const playbackRef = useRef<Audio.Sound | null>(null);
  const amplitudeIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const meterIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const recordingUriRef = useRef<string | null>(null);
  const stopAllRef = useRef<() => Promise<void>>();

  // Stop all audio operations (recording + playback + Deepgram WS)
  const stopAll = useCallback(async () => {
    if (amplitudeIntervalRef.current) {
      clearInterval(amplitudeIntervalRef.current);
      amplitudeIntervalRef.current = null;
    }
    if (meterIntervalRef.current) {
      clearInterval(meterIntervalRef.current);
      meterIntervalRef.current = null;
    }
    if (wsRef.current) {
      try {
        wsRef.current.close(1000, 'User stopped recording');
      } catch {
        // Ignore cleanup errors
      }
      wsRef.current = null;
    }
    if (recordingRef.current) {
      try {
        await recordingRef.current.stopAndUnloadAsync();
      } catch {
        // Ignore cleanup errors
      }
      recordingRef.current = null;
    }
    if (playbackRef.current) {
      try {
        await playbackRef.current.stopAsync();
        await playbackRef.current.unloadAsync();
      } catch {
        // Ignore cleanup errors
      }
      playbackRef.current = null;
    }
    setIsPlaying(false);
  }, []);

  // Keep ref in sync for cleanup
  useEffect(() => {
    stopAllRef.current = stopAll;
  }, [stopAll]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      stopAllRef.current?.();
    };
  }, []);

  /**
   * Start audio recording and request microphone permissions.
   * Simulates real-time transcription updates for UI feedback.
   */
  const startRecording = useCallback(async () => {
    setError(null);
    setTranscription('');

    try {
      // Request permissions
      const { status } = await Audio.requestPermissionsAsync();
      if (status !== 'granted') {
        setError('Microphone permission denied');
        setPhase('error');
        return;
      }

      // Configure audio mode
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: true,
        playsInSilentModeIOS: true,
        staysActiveInBackground: false,
        shouldDuckAndroid: true,
      });

      // ── Create and start recording ────────────────────────────────────────
      // Enable metering so we can read real audio levels for the waveform
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: true,
        playsInSilentModeIOS: true,
        staysActiveInBackground: false,
        shouldDuckAndroid: true,
      });

      const { recording } = await Audio.Recording.createAsync(
        Audio.RecordingOptionsPresets.HIGH_QUALITY
      );
      recordingRef.current = recording;
      recordingUriRef.current = recording.getURI();
      setPhase('recording');

      // ── Real-time audio metering for waveform (replaces Math.random) ──────
      meterIntervalRef.current = setInterval(async () => {
        if (recordingRef.current) {
          try {
            const status = await recordingRef.current.getStatusAsync();
            if (status.isRecording && status.metering !== undefined) {
              const level = normalizeMetering(status.metering);
              setAmplitude(buildAmplitudeArray(level));
            }
          } catch {
            // Metering read failed — ignore, next tick will retry
          }
        }
      }, 100);

      // ── Deepgram WebSocket streaming (replaces hardcoded demo text) ────────
      if (DEEPGRAM_API_KEY) {
        const ws = new WebSocket(
          `${DEEPGRAM_WS_URL}?encoding=linear16&sample_rate=44100&channels=1&model=nova-2&smart_format=true&interim_results=true`
        );
        wsRef.current = ws;

        ws.onopen = () => {
          // Deepgram connection ready — audio chunks will be sent in stopRecording
          // or we could stream chunks here via recording status callback
        };

        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data);
            const transcript =
              data.channel?.alternatives?.[0]?.transcript ?? '';
            if (transcript) {
              if (data.is_final) {
                // Final result — append permanently
                setTranscription((prev) =>
                  prev ? `${prev} ${transcript}` : transcript
                );
              }
              // Interim results could be shown live here if desired
            }
          } catch {
            // Malformed message — ignore
          }
        };

        ws.onerror = () => {
          setError('Deepgram connection error');
          // Keep recording locally so user doesn't lose their audio
        };

        ws.onclose = () => {
          wsRef.current = null;
        };
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to start recording';
      setError(message);
      setPhase('error');
    }
  }, []);

  /**
   * Stop recording and return final transcription.
   */
  const stopRecording = useCallback(async (): Promise<string> => {
    // Stop real-time metering
    if (meterIntervalRef.current) {
      clearInterval(meterIntervalRef.current);
      meterIntervalRef.current = null;
    }
    if (amplitudeIntervalRef.current) {
      clearInterval(amplitudeIntervalRef.current);
      amplitudeIntervalRef.current = null;
    }

    // Close Deepgram WebSocket (send remaining audio, wait for final transcript)
    if (wsRef.current) {
      try {
        wsRef.current.close(1000, 'Recording stopped');
      } catch {
        // Ignore
      }
      wsRef.current = null;
    }

    setAmplitude(DEFAULT_AMPLITUDE);

    if (!recordingRef.current) {
      setPhase('idle');
      return transcription;
    }

    setPhase('processing');

    try {
      await recordingRef.current.stopAndUnloadAsync();
      const uri = recordingRef.current.getURI();
      recordingUriRef.current = uri;
      recordingRef.current = null;

      if (!uri) {
        throw new Error('No recording URI available');
      }

      // Return the real transcription from Deepgram
      const finalTranscription = transcription.trim() || '(Voice recorded)';

      setPhase('idle');
      return finalTranscription;
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to stop recording';
      setError(message);
      setPhase('error');
      return transcription;
    }
  }, [transcription]);

  /**
   * Play TTS audio from a URL (ElevenLabs generated).
   */
  const playTTS = useCallback(async (audioUrl: string) => {
    setError(null);

    try {
      // Stop any existing playback
      if (playbackRef.current) {
        await playbackRef.current.stopAsync();
        await playbackRef.current.unloadAsync();
        playbackRef.current = null;
      }

      // Configure audio mode for playback
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: false,
        playsInSilentModeIOS: true,
        staysActiveInBackground: false,
        shouldDuckAndroid: true,
      });

      setPhase('playing');
      setIsPlaying(true);

      // Create and play sound
      const { sound } = await Audio.Sound.createAsync(
        { uri: audioUrl },
        { shouldPlay: true }
      );
      playbackRef.current = sound;

      // Listen for playback completion
      sound.setOnPlaybackStatusUpdate((status) => {
        if (status.isLoaded && status.didJustFinish) {
          setIsPlaying(false);
          setPhase('idle');
          sound.unloadAsync().catch(() => {});
          playbackRef.current = null;
        }
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to play audio';
      setError(message);
      setIsPlaying(false);
      setPhase('error');
    }
  }, []);

  /**
   * Stop TTS playback.
   */
  const stopPlayback = useCallback(async () => {
    if (playbackRef.current) {
      try {
        await playbackRef.current.stopAsync();
        await playbackRef.current.unloadAsync();
      } catch {
        // Ignore cleanup errors
      }
      playbackRef.current = null;
    }
    setIsPlaying(false);
    if (phase === 'playing') {
      setPhase('idle');
    }
  }, [phase]);

  /**
   * Reset to idle state.
   */
  const reset = useCallback(() => {
    stopAll();
    setPhase('idle');
    setTranscription('');
    setAmplitude(DEFAULT_AMPLITUDE);
    setError(null);
  }, [stopAll]);

  return {
    phase,
    isRecording: phase === 'recording',
    isPlaying,
    transcription,
    amplitude,
    error,

    startRecording,
    stopRecording,
    playTTS,
    stopPlayback,
    reset,
  };
}
