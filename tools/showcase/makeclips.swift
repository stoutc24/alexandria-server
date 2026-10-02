// makeclips.swift — synthetic video for the showcase server.
//
// Renders short H.264 clips (1280×720, 24 fps) of drifting gradients and
// soft shapes with the title set in the lower third, plus a gentle
// synthesized audio bed, and writes each as (1) a progressive MP4 and
// (2) an HLS fMP4 set (init.mp4, seg<n>.m4s, index.m3u8). Nobody's
// footage, nobody's music. macOS only (AVFoundation).
//
//   swift tools/showcase/makeclips.swift <outdir> "<id>|<title>|<subtitle>|<seconds>" ...

import Foundation
import AVFoundation
import CoreGraphics
import CoreText
import UniformTypeIdentifiers

let args = CommandLine.arguments
guard args.count >= 3 else {
    print("usage: makeclips <outdir> \"id|title|subtitle|seconds\" ...")
    exit(2)
}
let outRoot = URL(fileURLWithPath: args[1])
let fps: Int32 = 24
let width = 1280, height = 720
let sampleRate = 44_100.0

struct Clip { let id: String; let title: String; let subtitle: String; let seconds: Double }
let clips: [Clip] = args[2...].map {
    let p = $0.split(separator: "|", omittingEmptySubsequences: false).map(String.init)
    return Clip(id: p[0], title: p.count > 1 ? p[1] : p[0], subtitle: p.count > 2 ? p[2] : "", seconds: p.count > 3 ? Double(p[3]) ?? 60 : 60)
}

func hash(_ s: String) -> UInt32 { var h: UInt32 = 2166136261; for b in s.utf8 { h ^= UInt32(b); h = h &* 16777619 }; return h }

func palette(_ seed: UInt32) -> (CGColor, CGColor) {
    let pals: [((CGFloat, CGFloat, CGFloat), (CGFloat, CGFloat, CGFloat))] = [
        ((0.12, 0.23, 0.54), (0.15, 0.39, 0.92)), ((0.49, 0.18, 0.07), (0.79, 0.51, 0.16)),
        ((0.08, 0.33, 0.18), (0.13, 0.77, 0.37)), ((0.35, 0.11, 0.53), (0.66, 0.33, 0.97)),
        ((0.50, 0.11, 0.11), (0.94, 0.27, 0.27)), ((0.06, 0.46, 0.43), (0.18, 0.83, 0.75)),
    ]
    let p = pals[Int(seed) % pals.count]
    return (CGColor(red: p.0.0, green: p.0.1, blue: p.0.2, alpha: 1), CGColor(red: p.1.0, green: p.1.1, blue: p.1.2, alpha: 1))
}

func drawFrame(_ ctx: CGContext, clip: Clip, t: Double, seed: UInt32) {
    let (a, b) = palette(seed)
    let cs = CGColorSpaceCreateDeviceRGB()
    let grad = CGGradient(colorsSpace: cs, colors: [a, b] as CFArray, locations: [0, 1])!
    let ang = t * 0.15 + Double(seed % 360) * .pi / 180
    let cx = Double(width) / 2, cy = Double(height) / 2
    let r = 900.0
    ctx.drawLinearGradient(grad, start: CGPoint(x: cx - cos(ang) * r, y: cy - sin(ang) * r), end: CGPoint(x: cx + cos(ang) * r, y: cy + sin(ang) * r), options: [.drawsBeforeStartLocation, .drawsAfterEndLocation])
    // Drifting translucent discs.
    ctx.setFillColor(CGColor(red: 1, green: 1, blue: 1, alpha: 0.10))
    for i in 0..<7 {
        let ph = Double(i) * 1.7 + Double(seed % 97) * 0.1
        let x = cx + cos(t * 0.21 + ph) * 420 + sin(t * 0.07 + ph) * 120
        let y = cy + sin(t * 0.17 + ph) * 220
        let rad = 90.0 + 60.0 * sin(t * 0.3 + ph)
        ctx.fillEllipse(in: CGRect(x: x - rad, y: y - rad, width: rad * 2, height: rad * 2))
    }
    // Lower-third band + title.
    ctx.setFillColor(CGColor(red: 0, green: 0, blue: 0, alpha: 0.35))
    ctx.fill(CGRect(x: 0, y: 0, width: width, height: 190))
    func text(_ s: String, size: CGFloat, weight: CFString, y: CGFloat, alpha: CGFloat) {
        let font = CTFontCreateWithName(weight, size, nil)
        let attrs: [NSAttributedString.Key: Any] = [kCTFontAttributeName as NSAttributedString.Key: font, kCTForegroundColorAttributeName as NSAttributedString.Key: CGColor(red: 1, green: 1, blue: 1, alpha: alpha)]
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: s, attributes: attrs))
        ctx.textPosition = CGPoint(x: 80, y: y)
        CTLineDraw(line, ctx)
    }
    text(clip.title, size: 56, weight: "HelveticaNeue-Bold" as CFString, y: 96, alpha: 1)
    text(clip.subtitle, size: 28, weight: "HelveticaNeue" as CFString, y: 52, alpha: 0.85)
    // Timecode, small, top right — handy when scrubbing in a screenshot.
    let tc = String(format: "%02d:%02d", Int(t) / 60, Int(t) % 60)
    let f2 = CTFontCreateWithName("Menlo" as CFString, 26, nil)
    let l2 = CTLineCreateWithAttributedString(NSAttributedString(string: tc, attributes: [kCTFontAttributeName as NSAttributedString.Key: f2, kCTForegroundColorAttributeName as NSAttributedString.Key: CGColor(red: 1, green: 1, blue: 1, alpha: 0.6)]))
    ctx.textPosition = CGPoint(x: CGFloat(width) - 150, y: CGFloat(height) - 60)
    CTLineDraw(l2, ctx)
}

final class SegmentSink: NSObject, AVAssetWriterDelegate {
    let dir: URL
    var index = 0
    var durations: [Double] = []
    init(dir: URL) { self.dir = dir }
    func assetWriter(_ writer: AVAssetWriter, didOutputSegmentData segmentData: Data, segmentType: AVAssetSegmentType, segmentReport: AVAssetSegmentReport?) {
        switch segmentType {
        case .initialization:
            try? segmentData.write(to: dir.appendingPathComponent("init.mp4"))
        case .separable:
            try? segmentData.write(to: dir.appendingPathComponent("seg\(index).m4s"))
            let d = segmentReport?.trackReports.first?.duration.seconds ?? 6
            durations.append(d.isFinite && d > 0 ? d : 6)
            index += 1
        @unknown default: break
        }
    }
}

func audioSamples(seed: UInt32, from: Int, count: Int, total: Double) -> [Int16] {
    let keys = [196.0, 220.0, 246.9, 261.6]
    let root = keys[Int(seed) % keys.count]
    let chords: [[Double]] = [[0, 4, 7], [5, 9, 12], [7, 11, 14], [0, 4, 7]]
    var out = [Int16](repeating: 0, count: count)
    for i in 0..<count {
        let t = Double(from + i) / sampleRate
        let c = chords[Int(t / 2.8) % 4]
        let ph = (t.truncatingRemainder(dividingBy: 2.8)) / 2.8
        var v = 0.0
        for (n, s) in c.enumerated() {
            let f = root * pow(2, s / 12)
            v += (1.0 / Double(n + 1)) * (sin(2 * .pi * f * t) + 0.3 * sin(2 * .pi * 2 * f * t))
        }
        v *= sin(ph * .pi) * 0.18
        if t < 1 { v *= t }
        if total - t < 2 { v *= max(0, (total - t) / 2) }
        out[i] = Int16(max(-1, min(1, v)) * 30000)
    }
    return out
}

func makeAudioInput() -> AVAssetWriterInput {
    let settings: [String: Any] = [AVFormatIDKey: kAudioFormatMPEG4AAC, AVSampleRateKey: sampleRate, AVNumberOfChannelsKey: 1, AVEncoderBitRateKey: 96_000]
    let input = AVAssetWriterInput(mediaType: .audio, outputSettings: settings)
    input.expectsMediaDataInRealTime = false
    return input
}

func pcmBuffer(_ samples: [Int16], at time: CMTime) -> CMSampleBuffer? {
    var asbd = AudioStreamBasicDescription(mSampleRate: sampleRate, mFormatID: kAudioFormatLinearPCM, mFormatFlags: kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked, mBytesPerPacket: 2, mFramesPerPacket: 1, mBytesPerFrame: 2, mChannelsPerFrame: 1, mBitsPerChannel: 16, mReserved: 0)
    var format: CMAudioFormatDescription?
    CMAudioFormatDescriptionCreate(allocator: nil, asbd: &asbd, layoutSize: 0, layout: nil, magicCookieSize: 0, magicCookie: nil, extensions: nil, formatDescriptionOut: &format)
    var block: CMBlockBuffer?
    let bytes = samples.count * 2
    CMBlockBufferCreateWithMemoryBlock(allocator: nil, memoryBlock: nil, blockLength: bytes, blockAllocator: nil, customBlockSource: nil, offsetToData: 0, dataLength: bytes, flags: 0, blockBufferOut: &block)
    samples.withUnsafeBytes { raw in _ = CMBlockBufferReplaceDataBytes(with: raw.baseAddress!, blockBuffer: block!, offsetIntoDestination: 0, dataLength: bytes) }
    var sbuf: CMSampleBuffer?
    // The audio-specific constructor fills in constant PCM packet sizes.
    let status = CMAudioSampleBufferCreateReadyWithPacketDescriptions(
        allocator: nil, dataBuffer: block!, formatDescription: format!, sampleCount: samples.count,
        presentationTimeStamp: time, packetDescriptions: nil, sampleBufferOut: &sbuf)
    if status != noErr { print("audio sample buffer failed: \(status)") }
    return sbuf
}

func render(clip: Clip, hls: Bool) throws {
    let dir = outRoot.appendingPathComponent(clip.id)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let seed = hash(clip.id)
    let writer: AVAssetWriter
    let sink = SegmentSink(dir: dir)
    if hls {
        writer = AVAssetWriter(contentType: UTType(AVFileType.mp4.rawValue)!)
        writer.outputFileTypeProfile = .mpeg4AppleHLS
        writer.preferredOutputSegmentInterval = CMTime(seconds: 6, preferredTimescale: 1)
        writer.initialSegmentStartTime = .zero
        writer.delegate = sink
    } else {
        let file = dir.appendingPathComponent("clip.mp4")
        try? FileManager.default.removeItem(at: file)
        writer = try AVAssetWriter(outputURL: file, fileType: .mp4)
    }
    let video = AVAssetWriterInput(mediaType: .video, outputSettings: [
        AVVideoCodecKey: AVVideoCodecType.h264, AVVideoWidthKey: width, AVVideoHeightKey: height,
        AVVideoCompressionPropertiesKey: [AVVideoAverageBitRateKey: 3_000_000, AVVideoMaxKeyFrameIntervalKey: Int(fps) * 2, AVVideoProfileLevelKey: AVVideoProfileLevelH264HighAutoLevel]])
    video.expectsMediaDataInRealTime = false
    video.mediaTimeScale = 600
    let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: video, sourcePixelBufferAttributes: [
        kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA, kCVPixelBufferWidthKey as String: width, kCVPixelBufferHeightKey as String: height])
    let audio = makeAudioInput()
    writer.add(video); writer.add(audio)
    guard writer.startWriting() else { throw NSError(domain: "makeclips", code: 1, userInfo: [NSLocalizedDescriptionKey: writer.error?.localizedDescription ?? "startWriting"]) }
    writer.startSession(atSourceTime: .zero)

    let frames = Int(clip.seconds * Double(fps))
    let audioChunk = Int(sampleRate / Double(fps))
    // The documented pattern: each input pulls when the writer asks. The
    // writer interleaves the two by time on its own.
    let group = DispatchGroup()
    var failure: Error?
    let failLock = NSLock()
    func fail(_ e: Error) { failLock.lock(); if failure == nil { failure = e }; failLock.unlock() }
    var frame = 0
    group.enter()
    video.requestMediaDataWhenReady(on: DispatchQueue(label: "video")) {
        while video.isReadyForMoreMediaData {
            if frame >= frames { video.markAsFinished(); group.leave(); return }
            var pb: CVPixelBuffer?
            guard let pool = adaptor.pixelBufferPool else { usleep(1000); continue }
            CVPixelBufferPoolCreatePixelBuffer(nil, pool, &pb)
            guard let buffer = pb else { fail(NSError(domain: "makeclips", code: 7)); video.markAsFinished(); group.leave(); return }
            CVPixelBufferLockBaseAddress(buffer, [])
            let ctx = CGContext(data: CVPixelBufferGetBaseAddress(buffer), width: width, height: height, bitsPerComponent: 8, bytesPerRow: CVPixelBufferGetBytesPerRow(buffer), space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue)!
            drawFrame(ctx, clip: clip, t: Double(frame) / Double(fps), seed: seed)
            CVPixelBufferUnlockBaseAddress(buffer, [])
            let pts = CMTime(value: CMTimeValue(frame) * 600 / CMTimeValue(fps), timescale: 600)
            if !adaptor.append(buffer, withPresentationTime: pts) {
                fail(writer.error ?? NSError(domain: "makeclips", code: 5, userInfo: [NSLocalizedDescriptionKey: "video append failed at frame \(frame)"]))
                video.markAsFinished(); group.leave(); return
            }
            frame += 1
        }
    }
    var audioPos = 0
    let audioTotal = frames * audioChunk
    group.enter()
    audio.requestMediaDataWhenReady(on: DispatchQueue(label: "audio")) {
        while audio.isReadyForMoreMediaData {
            if audioPos >= audioTotal { audio.markAsFinished(); group.leave(); return }
            let samples = audioSamples(seed: seed, from: audioPos, count: audioChunk, total: clip.seconds)
            guard let sb = pcmBuffer(samples, at: CMTime(value: CMTimeValue(audioPos), timescale: Int32(sampleRate))) else {
                fail(NSError(domain: "makeclips", code: 8)); audio.markAsFinished(); group.leave(); return
            }
            if !audio.append(sb) {
                fail(writer.error ?? NSError(domain: "makeclips", code: 6, userInfo: [NSLocalizedDescriptionKey: "audio append failed at \(audioPos)"]))
                audio.markAsFinished(); group.leave(); return
            }
            audioPos += audioChunk
        }
    }
    if group.wait(timeout: .now() + 600) == .timedOut { throw NSError(domain: "makeclips", code: 9, userInfo: [NSLocalizedDescriptionKey: "render timed out at frame \(frame)"]) }
    if let failure { throw failure }
    let done = DispatchSemaphore(value: 0)
    writer.finishWriting { done.signal() }
    done.wait()
    if writer.status == .failed { throw writer.error ?? NSError(domain: "makeclips", code: 2) }
    if hls {
        var lines = ["#EXTM3U", "#EXT-X-VERSION:7", "#EXT-X-TARGETDURATION:\(Int((sink.durations.max() ?? 6).rounded(.up)))", "#EXT-X-MEDIA-SEQUENCE:0", "#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-INDEPENDENT-SEGMENTS", "#EXT-X-MAP:URI=\"init.mp4\""]
        for (i, d) in sink.durations.enumerated() { lines.append(String(format: "#EXTINF:%.3f,", d)); lines.append("seg\(i).m4s") }
        lines.append("#EXT-X-ENDLIST")
        try (lines.joined(separator: "\n") + "\n").write(to: dir.appendingPathComponent("index.m3u8"), atomically: true, encoding: .utf8)
    }
}

for clip in clips {
    let started = Date()
    try render(clip: clip, hls: false)
    try render(clip: clip, hls: true)
    print(String(format: "%@: %.0fs rendered in %.1fs", clip.id, clip.seconds, Date().timeIntervalSince(started)))
}
