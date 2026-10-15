#!/usr/bin/env python3
"""Diction pass, then a constrained sanitizer. Nothing is returned until both finish."""

import hmac
import json
import logging
import math
import os
import re
import tempfile
import threading
import time
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import torch
from huggingface_hub import HfApi, constants as hf_constants, snapshot_download
from qwen_asr import Qwen3ASRModel
from transformers import AutoModelForCausalLM, AutoTokenizer, BitsAndBytesConfig

HERE = os.path.dirname(os.path.abspath(__file__))
HOST = "127.0.0.1"
PORT = 8765
LISTEN = [HOST]
ASR_ID = "Qwen/Qwen3-ASR-1.7B"
SAN_ID = "Qwen/Qwen3-4B-Instruct-2507"
LANGUAGE = "English"
GLOSSARY_PATH = os.path.join(HERE, "glossary.txt")
TOKEN_PATH = os.path.join(HERE, "token")
MAX_AUDIO = 25 * 1024 * 1024

log = logging.getLogger("patter")
GPU = threading.Lock()
ASR = None
TOK = None
SAN = None

SCRATCH_UTTERANCES = {
    "scratch that",
    "undo that",
    "delete that",
    "scratch it",
    "undo it",
}
SCRATCH_WORDS = {"scratch", "undo", "delete", "erase", "cancel", "nevermind"}


def apply_config():
    """Optional config.json beside the server. Environment overrides the file."""
    global HOST, PORT, LISTEN, ASR_ID, SAN_ID, LANGUAGE, GLOSSARY_PATH, TOKEN_PATH
    listen = None
    path = os.environ.get("PATTER_CONFIG", os.path.join(HERE, "config.json"))
    if os.path.isfile(path):
        with open(path, encoding="utf-8") as f:
            cfg = json.load(f)
        if cfg.get("host"):
            HOST = str(cfg["host"])
        if cfg.get("listen"):
            listen = [str(h) for h in cfg["listen"] if str(h)]
        if "port" in cfg:
            PORT = int(cfg["port"])
        if cfg.get("asr"):
            ASR_ID = str(cfg["asr"])
        if cfg.get("sanitizer"):
            SAN_ID = str(cfg["sanitizer"])
        if cfg.get("language"):
            LANGUAGE = str(cfg["language"])
        if cfg.get("glossary"):
            GLOSSARY_PATH = str(cfg["glossary"])
            if not os.path.isabs(GLOSSARY_PATH):
                GLOSSARY_PATH = os.path.join(HERE, GLOSSARY_PATH)
        if cfg.get("token_file"):
            TOKEN_PATH = str(cfg["token_file"])
            if not os.path.isabs(TOKEN_PATH):
                TOKEN_PATH = os.path.join(HERE, TOKEN_PATH)
    if os.environ.get("PATTER_HOST"):
        HOST = os.environ["PATTER_HOST"]
    if os.environ.get("PATTER_PORT"):
        PORT = int(os.environ["PATTER_PORT"])
    if os.environ.get("PATTER_ASR"):
        ASR_ID = os.environ["PATTER_ASR"]
    if os.environ.get("PATTER_SANITIZER"):
        SAN_ID = os.environ["PATTER_SANITIZER"]
    if os.environ.get("PATTER_LANGUAGE"):
        LANGUAGE = os.environ["PATTER_LANGUAGE"]
    if os.environ.get("PATTER_LISTEN"):
        LISTEN = [h for h in os.environ["PATTER_LISTEN"].split(",") if h]
    elif listen:
        LISTEN = listen
    else:
        LISTEN = [HOST]


def glossary():
    words = []
    try:
        with open(GLOSSARY_PATH, encoding="utf-8") as f:
            for line in f:
                word = line.strip()
                if word and not word.startswith("#"):
                    words.append(word)
    except OSError:
        pass
    return words


def norm_utterance(text):
    cleaned = text.lower().replace(".", " ").replace(",", " ").replace("!", " ").replace("?", " ")
    return " ".join(cleaned.split())


# One word on each side of the marker. "have no idea" must stay.
RETRACT_SKIP = {
    "a", "an", "the", "to", "of", "for", "on", "in", "at", "by", "with", "from",
    "and", "or", "but", "if", "so", "it", "its", "that", "this", "my", "your",
    "we", "i", "you", "he", "she", "they", "there", "here", "is", "are", "was",
    "were", "am", "be", "been", "have", "has", "had", "do", "does", "did", "not",
    "um", "uh", "er", "ah", "oh", "yeah",
}
RETRACT_MARKERS = (
    ("scratch", "that"),
    ("i", "mean"),
    ("sorry",),
    ("no",),
)
RETRACT_BOUND = {"and", "but", "then", "so", "or"}


def _bare(word):
    return re.sub(r"^[^\w]+|[^\w]+$", "", word).lower()


def retract(raw):
    words = raw.split()
    while True:
        bares = [_bare(w) for w in words]
        hit = None
        i = 0
        while i < len(words):
            for candidate in RETRACT_MARKERS:
                n = len(candidate)
                if bares[i:i + n] != list(candidate):
                    continue
                pre = i - 1
                post = i + n
                if pre < 0 or post >= len(words):
                    continue
                nxt = post + 1
                if nxt < len(words) and bares[nxt] not in RETRACT_BOUND:
                    continue
                if not bares[pre] or bares[pre] in RETRACT_SKIP or not bares[post]:
                    continue
                hit = (pre, post)
            i += 1
        if hit is None:
            break
        pre, post = hit
        words = words[:pre] + words[post:]
    return " ".join(words)


def apply_glossary_case(text):
    names = sorted(glossary(), key=len, reverse=True)
    for name in names:
        text = re.sub(rf"\b{re.escape(name)}\b", name, text, flags=re.IGNORECASE)
    return text


def token():
    try:
        with open(TOKEN_PATH, encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return ""


# Loader state, read by /health. Only one load runs at a time.
LOAD_LOCK = threading.Lock()
LOAD = {"state": "idle", "model": "", "percent": -1, "error": ""}
LOADING = False


def set_load(state, model="", percent=-1, error=""):
    with LOAD_LOCK:
        LOAD.update({"state": state, "model": model, "percent": percent, "error": error})


def load_snapshot():
    with LOAD_LOCK:
        return dict(LOAD)


def repo_bytes(repo_id):
    try:
        info = HfApi().model_info(repo_id, files_metadata=True)
        return sum(f.size or 0 for f in info.siblings or [])
    except Exception:
        return 0


def cached_bytes(repo_id):
    blobs = os.path.join(hf_constants.HF_HUB_CACHE, "models--" + repo_id.replace("/", "--"), "blobs")
    total = 0
    try:
        for name in os.listdir(blobs):
            try:
                total += os.path.getsize(os.path.join(blobs, name))
            except OSError:
                pass
    except OSError:
        pass
    return total


def fetch(repo_id):
    """Return a local directory for repo_id, downloading it with a byte count on /health."""
    if os.path.isdir(repo_id):
        return repo_id
    try:
        return snapshot_download(repo_id, local_files_only=True)
    except Exception:
        pass
    total = repo_bytes(repo_id)
    set_load("downloading", repo_id, 0)
    done = {}

    def run():
        try:
            done["path"] = snapshot_download(repo_id)
        except Exception as e:
            done["error"] = e

    t = threading.Thread(target=run, daemon=True)
    t.start()
    while t.is_alive():
        t.join(1.0)
        if total > 0:
            set_load("downloading", repo_id, min(99, cached_bytes(repo_id) * 100 // total))
    if "error" in done:
        raise done["error"]
    return done["path"]


def load_models(asr_id=None, san_id=None):
    """Fetch, then swap under the GPU lock. Old models stay in use until the swap."""
    global ASR, TOK, SAN, ASR_ID, SAN_ID, LOADING
    asr_id = asr_id or ASR_ID
    san_id = san_id or SAN_ID
    cur = asr_id
    try:
        new_asr = new_san = None
        if ASR is None or asr_id != ASR_ID:
            asr_path = fetch(asr_id)
            set_load("loading", asr_id)
            with GPU:
                if ASR is not None:
                    ASR = None
                    torch.cuda.empty_cache()
                new_asr = Qwen3ASRModel.from_pretrained(
                    asr_path,
                    dtype=torch.bfloat16,
                    device_map="cuda:0",
                    max_inference_batch_size=1,
                    max_new_tokens=512,
                )
                ASR, ASR_ID = new_asr, asr_id
                torch.cuda.empty_cache()
            log.info("loaded %s", asr_id)
        if SAN is None or san_id != SAN_ID:
            cur = san_id
            san_path = fetch(san_id)
            set_load("loading", san_id)
            quant = BitsAndBytesConfig(
                load_in_4bit=True,
                bnb_4bit_compute_dtype=torch.bfloat16,
                bnb_4bit_quant_type="nf4",
                bnb_4bit_use_double_quant=True,
            )
            with GPU:
                if SAN is not None:
                    SAN = None
                    TOK = None
                    torch.cuda.empty_cache()
                tok = AutoTokenizer.from_pretrained(san_path)
                new_san = AutoModelForCausalLM.from_pretrained(
                    san_path,
                    quantization_config=quant,
                    device_map={"": 0},
                )
                new_san.eval()
                TOK, SAN, SAN_ID = tok, new_san, san_id
                torch.cuda.empty_cache()
            log.info("loaded %s", san_id)
        free, total = torch.cuda.mem_get_info()
        log.info("gpu free %d MiB of %d MiB", free // (1024 * 1024), total // (1024 * 1024))
        set_load("ready")
    except Exception as e:
        log.exception("load failed")
        set_load("failed", cur, -1, str(e)[:200])
    finally:
        LOADING = False


def start_load(asr_id=None, san_id=None):
    global LOADING
    with LOAD_LOCK:
        if LOADING:
            return False
        LOADING = True
    threading.Thread(target=load_models, args=(asr_id, san_id), daemon=True).start()
    return True


def asr_context():
    names = ", ".join(glossary())
    return "Spell these names exactly when they are spoken: " + names + "."


# 5s stays under the forward that ran the audio convolution out of scratch
# space on a 12GB card with both models resident. Longer audio is several
# of these, and the finished text is not fed through the GPU again.
CHUNK_BYTES = 5 * 16000 * 2
OVERLAP_BYTES = 16000
SR = 16000


def pcm_rms(pcm):
    if len(pcm) < 2:
        return 0.0
    step = 2 * max(1, len(pcm) // 4000)
    acc = 0.0
    n = 0
    for i in range(0, len(pcm) - 1, step):
        s = int.from_bytes(pcm[i : i + 2], "little", signed=True)
        acc += s * s
        n += 1
    if n == 0:
        return 0.0
    return math.sqrt(acc / n)


def snap_quiet(pcm, start, end):
    end = end & ~1
    start = start & ~1
    if end - start < 3 * 32000:
        return end
    width = 6400
    back = 32000
    earliest = max(end - back, start + 3 * 32000)
    pos = end
    while pos - width >= earliest:
        if pcm_rms(pcm[pos - width : pos]) < 220:
            return pos & ~1
        pos -= 640
    return end


def wav_pcm(path):
    with wave.open(path, "rb") as w:
        return w.readframes(w.getnframes())


def write_wav(path, pcm):
    if len(pcm) % 2:
        pcm = pcm[:-1]
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(pcm)


def stitch_text(left, right):
    left = " ".join(left.split())
    right = " ".join(right.split())
    if not left:
        return right
    if not right:
        return left
    lw = left.split()
    rw = right.split()
    maxk = min(len(lw), len(rw), 8)

    def bare(word):
        return word.lower().strip(".,!?;:\"'")

    best = 0
    for k in range(maxk, 0, -1):
        if [bare(w) for w in lw[-k:]] == [bare(w) for w in rw[:k]]:
            best = k
            break
    rw = rw[best:]
    if not rw:
        return left
    return left + " " + " ".join(rw)


def transcribe_pcm(pcm):
    fd, path = tempfile.mkstemp(prefix="patter-chunk-", suffix=".wav")
    os.close(fd)
    try:
        write_wav(path, pcm)
        with GPU:
            torch.cuda.empty_cache()
            results = ASR.transcribe(
                audio=path,
                context=asr_context(),
                language=LANGUAGE,
            )
        return (results[0].text or "").strip()
    except RuntimeError:
        with GPU:
            torch.cuda.empty_cache()
        raise
    finally:
        try:
            os.remove(path)
        except OSError:
            pass


def transcribe(path):
    t0 = time.monotonic()
    pcm = wav_pcm(path)
    if len(pcm) <= CHUNK_BYTES + OVERLAP_BYTES:
        text = transcribe_pcm(pcm)
        return text, int((time.monotonic() - t0) * 1000)
    texts = []
    pos = 0
    while pos < len(pcm):
        end = min(len(pcm), pos + CHUNK_BYTES)
        if end < len(pcm):
            end = snap_quiet(pcm, pos, end)
        texts.append(transcribe_pcm(pcm[pos:end]))
        if end >= len(pcm):
            break
        nxt = end - OVERLAP_BYTES
        if nxt <= pos:
            nxt = end
        pos = nxt & ~1
    log.info("transcribe chunks=%d pcm_bytes=%d", len(texts), len(pcm))
    text = ""
    for piece in texts:
        text = stitch_text(text, piece)
    return text, int((time.monotonic() - t0) * 1000)


SANITIZE_SYSTEM = """You edit a speech transcript before it is typed. Output only the cleaned text, or the single word SCRATCH.

Rules:
- Remove fillers and backchannels: um, uh, er, ah, uh huh, oh yeah.
- A retraction replaces what came just before it. If the speaker says "no", "sorry", "I mean", or "scratch that" and then gives the replacement, drop the retracted words and the retraction phrase. Keep only the replacement.
- If the whole utterance only asks to undo the last insertion, output exactly SCRATCH.
- Add punctuation and sentence capitalization.
- When a word is one of the names in the user message, or a mishearing of one, spell it with the exact characters from that list. Do not change those characters.
- Do not paraphrase. Do not add words the speaker did not say. Do not summarize.
- Output nothing except the cleaned text or SCRATCH.

Examples:
Transcript: um meet on tuesday no wednesday
Output: Meet on Wednesday.

Transcript: the meeting is monday sorry tuesday
Output: The meeting is Tuesday.

Transcript: check the atlas on northwind
Output: Check the Atlas on Northwind.

Transcript: ping harbor then pebble i mean cedar
Output: Ping harbor then cedar.

Transcript: the vyos box named harbor is the router
Output: The VyOS box named harbor is the router.

Transcript: scratch that
Output: SCRATCH"""


def sanitize(raw):
    if norm_utterance(raw) in SCRATCH_UTTERANCES:
        return "SCRATCH", 0
    words = raw.split()
    if len(words) <= 140:
        return sanitize_piece(raw)
    size = 120
    step = 100
    t0 = time.monotonic()
    parts = []
    for start in range(0, len(words), step):
        piece = " ".join(words[start : start + size])
        cleaned, _ = sanitize_piece(piece)
        if norm_utterance(cleaned) == "scratch":
            cleaned = piece
        parts.append(cleaned)
        if start + size >= len(words):
            break
    text = ""
    for piece in parts:
        text = stitch_text(text, piece)
    return text, int((time.monotonic() - t0) * 1000)


def sanitize_piece(raw):
    prepared = retract(raw)
    names = ", ".join(glossary())
    user = "Names: " + names + "\n\nTranscript:\n" + prepared
    messages = [
        {"role": "system", "content": SANITIZE_SYSTEM},
        {"role": "user", "content": user},
    ]
    prompt = TOK.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
    inputs = TOK(prompt, return_tensors="pt").to(SAN.device)
    t0 = time.monotonic()
    with GPU:
        torch.cuda.empty_cache()
        with torch.inference_mode():
            out = SAN.generate(
                **inputs,
                max_new_tokens=min(512, max(32, len(raw.split()) * 3 + 16)),
                do_sample=False,
            )
    new_tokens = out[0, inputs["input_ids"].shape[1] :]
    text = TOK.decode(new_tokens, skip_special_tokens=True).strip()
    if text.startswith("```") and text.endswith("```"):
        text = text.strip("`").strip()
    if (text.startswith('"') and text.endswith('"')) or (text.startswith("'") and text.endswith("'")):
        text = text[1:-1].strip()
    text = apply_glossary_case(text)
    # A rewrite that balloons the utterance is the sanitizer ignoring the rules.
    if len(text) > len(raw) * 2 + 40 and norm_utterance(text) != "scratch":
        text = raw
    return text, int((time.monotonic() - t0) * 1000)


def decide(raw, cleaned):
    if not raw.strip() or not cleaned.strip():
        return "none", ""
    words = norm_utterance(raw).split()
    # A scratch needs an undo word. The model says SCRATCH for unknown words too.
    if norm_utterance(raw) in SCRATCH_UTTERANCES or (
        norm_utterance(cleaned) == "scratch"
        and len(words) <= 4
        and (set(words) & SCRATCH_WORDS or words[:2] == ["never", "mind"])
    ):
        return "scratch", ""
    if norm_utterance(cleaned) == "scratch":
        cleaned = raw.strip()
    if cleaned[-1].isalnum():
        cleaned = cleaned + " "
    elif not cleaned.endswith((" ", "\n")):
        cleaned = cleaned + " "
    return "insert", cleaned


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        log.info("%s " + fmt, self.address_string(), *args)

    def _send(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _authorized(self):
        got = self.headers.get("Authorization", "")
        secret = token()
        if not secret:
            return False
        return hmac.compare_digest(got, "Bearer " + secret)

    def do_GET(self):
        if self.path != "/health":
            self._send(404, {"error": "not found"})
            return
        ready = ASR is not None and SAN is not None and not LOADING
        snap = load_snapshot()
        self._send(200 if ready else 503, {
            "ready": ready,
            "asr": ASR_ID,
            "sanitizer": SAN_ID,
            "language": LANGUAGE,
            "state": snap["state"],
            "model": snap["model"],
            "percent": snap["percent"],
            "error": snap["error"],
        })

    def _settings(self):
        global LANGUAGE
        length = int(self.headers.get("Content-Length", "0") or 0)
        if length <= 0 or length > 8192:
            self._send(400, {"error": "bad settings"})
            return
        try:
            incoming = json.loads(self.rfile.read(length).decode())
        except (json.JSONDecodeError, UnicodeError):
            self._send(400, {"error": "bad settings"})
            return
        if not isinstance(incoming, dict):
            self._send(400, {"error": "bad settings"})
            return
        path = os.environ.get("PATTER_CONFIG", os.path.join(HERE, "config.json"))
        cfg = {}
        if os.path.isfile(path):
            with open(path, encoding="utf-8") as f:
                cfg = json.load(f)
        if not isinstance(cfg, dict):
            cfg = {}
        restart = False
        asr = str(incoming.get("asr") or "").strip()
        sanitizer = str(incoming.get("sanitizer") or "").strip()
        language = str(incoming.get("language") or "").strip()
        if asr:
            restart = restart or asr != ASR_ID
            cfg["asr"] = asr
        if sanitizer:
            restart = restart or sanitizer != SAN_ID
            cfg["sanitizer"] = sanitizer
        if language:
            cfg["language"] = language
            LANGUAGE = language
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(cfg, f, indent=2)
            f.write("\n")
        os.replace(tmp, path)
        log.info("settings stored restart_models=%s", restart)
        if restart and not start_load(asr or None, sanitizer or None):
            self._send(409, {"error": "a model is already loading"})
            return
        self._send(200, {
            "ok": True,
            "restart_models": restart,
            "asr": ASR_ID,
            "sanitizer": SAN_ID,
            "language": LANGUAGE,
        })

    def do_POST(self):
        if not self._authorized():
            self._send(401, {"error": "unauthorized"})
            return
        if self.path == "/v1/settings":
            self._settings()
            return
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > MAX_AUDIO:
            self._send(400, {"error": "bad audio length"})
            return
        body = self.rfile.read(length)
        if self.path == "/v1/sanitize":
            if SAN is None:
                self._send(503, {"error": "model " + load_snapshot()["state"]})
                return
            raw = body.decode("utf-8", errors="replace").strip()
            try:
                cleaned, san_ms = sanitize(raw)
            except Exception:
                log.exception("sanitize failed, using raw")
                cleaned, san_ms = raw, 0
            action, text = decide(raw, cleaned)
            log.info("sanitize action=%s raw_chars=%d out_chars=%d san_ms=%d", action, len(raw), len(text), san_ms)
            self._send(200, {"action": action, "text": text, "raw": raw})
            return
        if self.path not in ("/v1/dictate", "/v1/transcribe"):
            self._send(404, {"error": "not found"})
            return
        if not body.startswith(b"RIFF"):
            self._send(400, {"error": "expected wav"})
            return
        if ASR is None or SAN is None:
            self._send(503, {"error": "model " + load_snapshot()["state"]})
            return
        fd, path = tempfile.mkstemp(prefix="patter-", suffix=".wav")
        try:
            with os.fdopen(fd, "wb") as f:
                f.write(body)
            try:
                raw, asr_ms = transcribe(path)
            except Exception:
                log.exception("dictate failed")
                try:
                    torch.cuda.empty_cache()
                except Exception:
                    pass
                self._send(500, {"error": "dictate failed"})
                return
        finally:
            try:
                os.remove(path)
            except OSError:
                pass
        if self.path == "/v1/transcribe":
            log.info("transcribe raw_chars=%d asr_ms=%d", len(raw), asr_ms)
            self._send(200, {"raw": raw, "asr_ms": asr_ms})
            return
        if raw:
            try:
                cleaned, san_ms = sanitize(raw)
            except Exception:
                log.exception("sanitize failed, using raw")
                cleaned, san_ms = raw, 0
        else:
            cleaned, san_ms = "", 0
        action, text = decide(raw, cleaned)
        log.info(
            "dictate action=%s raw_chars=%d out_chars=%d asr_ms=%d san_ms=%d",
            action, len(raw), len(text), asr_ms, san_ms,
        )
        self._send(200, {"action": action, "text": text, "raw": raw})


def _serve(host):
    httpd = ThreadingHTTPServer((host, PORT), Handler)
    log.info("listening on %s:%d", host, PORT)
    httpd.serve_forever()


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    apply_config()
    start_load()
    for host in LISTEN[1:]:
        threading.Thread(target=_serve, args=(host,), daemon=True).start()
    _serve(LISTEN[0])


if __name__ == "__main__":
    main()
