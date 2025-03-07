package lksdk

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v3"

	"github.com/LonglyCode/protocol/livekit"
)

type RemoteParticipant struct {
	baseParticipant
	pliWriter PLIWriter
	client    *SignalClient
}

func newRemoteParticipant(pi *livekit.ParticipantInfo, roomCallback *RoomCallback, client *SignalClient, pliWriter PLIWriter) *RemoteParticipant {
	p := &RemoteParticipant{
		baseParticipant: *newBaseParticipant(roomCallback),
		client:          client,
		pliWriter:       pliWriter,
	}
	p.updateInfo(pi)
	return p
}

func (p *RemoteParticipant) updateInfo(pi *livekit.ParticipantInfo) {
	p.baseParticipant.updateInfo(pi, p)
	// update tracks
	validPubs := make(map[string]TrackPublication)
	p.tracks.Range(func(key, value interface{}) bool {
		validPubs[key.(string)] = value.(TrackPublication)
		return true
	})
	newPubs := make(map[string]TrackPublication)

	for _, ti := range pi.Tracks {
		pub := p.getPublication(ti.Sid)
		if pub == nil {
			// new track
			remotePub := &RemoteTrackPublication{}
			remotePub.updateInfo(ti)
			remotePub.client = p.client
			remotePub.participantID = p.sid
			p.addPublication(remotePub)
			newPubs[ti.Sid] = remotePub
			pub = remotePub
		} else {
			wasMuted := pub.IsMuted()
			pub.updateInfo(ti)
			if ti.Muted != wasMuted {
				if ti.Muted {
					p.Callback.OnTrackMuted(pub, p)
					p.roomCallback.OnTrackMuted(pub, p)
				} else {
					p.Callback.OnTrackUnmuted(pub, p)
					p.roomCallback.OnTrackUnmuted(pub, p)
				}
			}
		}
		validPubs[ti.Sid] = pub
	}

	// send events for new publications
	for _, pub := range newPubs {
		p.Callback.OnTrackPublished(pub.(*RemoteTrackPublication), p)
		p.roomCallback.OnTrackPublished(pub.(*RemoteTrackPublication), p)
	}

	var toUnpublish []string
	p.tracks.Range(func(key, value interface{}) bool {
		sid := key.(string)
		if validPubs[sid] == nil {
			toUnpublish = append(toUnpublish, sid)
		}
		return true
	})
	for _, sid := range toUnpublish {
		p.unpublishTrack(sid, true)
	}
}

func (p *RemoteParticipant) addSubscribedMediaTrack(track *webrtc.TrackRemote, trackSID string,
	receiver *webrtc.RTPReceiver) {
	pub := p.getPublication(trackSID)
	if pub == nil {
		// wait for metadata to arrive
		go func() {
			start := time.Now()
			for time.Since(start) < 5*time.Second {
				pub := p.getPublication(trackSID)
				if pub != nil {
					p.addSubscribedMediaTrack(track, trackSID, receiver)
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			p.Callback.OnTrackSubscriptionFailed(trackSID, p)
			p.roomCallback.OnTrackSubscriptionFailed(trackSID, p)
		}()
		return
	}
	pub.setReceiverAndTrack(receiver, track)

	logger.Infow("track subscribed",
		"participant", p.Identity(), "track", pub.sid,
		"kind", pub.kind)
	p.Callback.OnTrackSubscribed(track, pub, p)
	p.roomCallback.OnTrackSubscribed(track, pub, p)
}

func (p *RemoteParticipant) getPublication(trackSID string) *RemoteTrackPublication {
	if pub, ok := p.baseParticipant.getPublication(trackSID).(*RemoteTrackPublication); ok {
		return pub
	}
	return nil
}

func (p *RemoteParticipant) unpublishTrack(sid string, sendUnpublish bool) {
	pub := p.getPublication(sid)
	if pub == nil {
		return
	}

	p.lock.Lock()
	defer p.lock.Unlock()
	switch pub.Kind() {
	case TrackKindAudio:
		p.audioTracks.Delete(sid)
	case TrackKindVideo:
		p.videoTracks.Delete(sid)
	}

	track := pub.TrackRemote()
	if track != nil {
		p.Callback.OnTrackUnsubscribed(track, pub, p)
		p.roomCallback.OnTrackUnsubscribed(track, pub, p)
	}
	if sendUnpublish {
		p.Callback.OnTrackUnpublished(pub, p)
		p.roomCallback.OnTrackUnpublished(pub, p)
	}
}

func (p *RemoteParticipant) WritePLI(ssrc webrtc.SSRC) {
	p.pliWriter(ssrc)
}

func (p *RemoteParticipant) unpublishAllTracks() {

	p.tracks.Range(func(_, value interface{}) bool {
		pub := value.(TrackPublication)
		if remoteTrack, ok := pub.Track().(*webrtc.TrackRemote); ok {
			if pub.Track() != nil {
				p.Callback.OnTrackUnsubscribed(remoteTrack, pub.(*RemoteTrackPublication), p)
				p.roomCallback.OnTrackUnsubscribed(remoteTrack, pub.(*RemoteTrackPublication), p)
			}
		}
		return true
	})
	eraseSyncMap(p.tracks)
	eraseSyncMap(p.audioTracks)
	eraseSyncMap(p.videoTracks)
}

func eraseSyncMap(m *sync.Map) {
	m.Range(func(key interface{}, value interface{}) bool {
		m.Delete(key)
		return true
	})
}
