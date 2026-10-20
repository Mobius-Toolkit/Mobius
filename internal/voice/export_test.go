package voice

func (w *Whisper) IsDownloading() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.downloading
}
