import os
import time
import json
import random
import subprocess
import shutil
from io import BytesIO

from fastapi import FastAPI
from fastapi.responses import StreamingResponse, FileResponse, JSONResponse

# -----------------------------
# Global state
# -----------------------------

app = FastAPI(title="Pi Sensor Service")

START_TS = time.time()
LAST_SNAPSHOT_OK = False
LAST_SNAPSHOT_TS = None
LAST_SNAPSHOT_PATH = "/tmp/pi_snapshot.jpg"

# Detect whether rpicam-still is available on this Pi
try:
    CAMERA_AVAILABLE = shutil.which("rpicam-still") is not None
except Exception:
    CAMERA_AVAILABLE = False

# Basic config (can be overridden via env)
DEVICE_ID = os.getenv("PI_DEVICE_ID", "localsense-pi-1")
LOCATION_LAT = float(os.getenv("PI_LOCATION_LAT", "12.9716"))   # example: Bengaluru
LOCATION_LON = float(os.getenv("PI_LOCATION_LON", "77.5946"))
LOCATION_LABEL = os.getenv("PI_LOCATION_LABEL", "home-node")

# -----------------------------
# Fake env sensor (temp/humidity)
# -----------------------------

def generate_reading():
    """Fake sensor: returns a single temperature + humidity reading."""
    return {
        "ts": int(time.time()),
        "temperature": round(random.uniform(24.0, 30.0), 2),
        "humidity": round(random.uniform(40.0, 70.0), 2),
    }


@app.get("/reading")
def reading():
    """
    One-shot reading.
    Example:
    { "ts": 1730000000, "temperature": 26.5, "humidity": 53.2 }
    """
    return generate_reading()


def stream_generator():
    """Continuous stream: one JSON line every 5 seconds."""
    while True:
        payload = generate_reading()
        yield json.dumps(payload) + "\n"
        time.sleep(5)


@app.get("/stream")
def stream():
    """
    Long-lived HTTP stream of readings (NDJSON).
    """
    return StreamingResponse(stream_generator(), media_type="application/json")


# -----------------------------
# PiCam snapshot
# -----------------------------

def capture_snapshot():
    """
    Capture a snapshot using rpicam-still into LAST_SNAPSHOT_PATH
    and return an open file object ready to stream.
    """
    global LAST_SNAPSHOT_TS, LAST_SNAPSHOT_OK

    if not CAMERA_AVAILABLE:
        raise RuntimeError("Camera not available or rpicam-still not installed")

    # Keep it as simple as possible – this is known to work from your CLI.
    cmd = [
        "rpicam-still",
        "-o",
        LAST_SNAPSHOT_PATH,
    ]

    try:
        result = subprocess.run(
            cmd,
            check=True,
            timeout=20,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        # Log for debugging
        if result.stdout:
            print("[snapshot] rpicam-still stdout:", result.stdout)
        if result.stderr:
            print("[snapshot] rpicam-still stderr:", result.stderr)

        LAST_SNAPSHOT_TS = int(time.time())
        LAST_SNAPSHOT_OK = True
    except subprocess.CalledProcessError as e:
        LAST_SNAPSHOT_OK = False
        print("[snapshot] rpicam-still failed:", e, "stderr:", e.stderr)
        raise
    except Exception as e:
        LAST_SNAPSHOT_OK = False
        print("[snapshot] unexpected error:", e)
        raise

    return open(LAST_SNAPSHOT_PATH, "rb")


@app.get("/snapshot")
def snapshot():
    """
    Capture and return the latest snapshot as image/jpeg.
    """
    file_obj = capture_snapshot()
    # We return the saved file path via FileResponse
    return FileResponse(LAST_SNAPSHOT_PATH, media_type="image/jpeg", filename="snapshot.jpg")


# -----------------------------
# Derived metrics (brightness)
# -----------------------------

def compute_brightness_from_jpeg(path: str) -> float:
    """
    Approximate brightness by averaging grayscale pixel values.
    Requires Pillow (pip install pillow).
    """
    from PIL import Image  # local import to avoid hard dependency at import time

    img = Image.open(path).convert("L")  # grayscale
    pixels = list(img.getdata())
    if not pixels:
        return 0.0
    avg = sum(pixels) / len(pixels)  # 0-255
    # Normalize to 0-10 for a simple score
    return round((avg / 255.0) * 10.0, 2)


@app.get("/metrics")
def metrics():
    """
    Return derived metrics from the latest snapshot *if available*.
    If no snapshot or error, fall back to a synthetic brightness value.
    """
    now_ts = int(time.time())

    brightness: float

    try:
        if os.path.exists(LAST_SNAPSHOT_PATH):
            # Try to compute from last snapshot
            brightness = compute_brightness_from_jpeg(LAST_SNAPSHOT_PATH)
        else:
            # No snapshot yet – use synthetic brightness
            brightness = round(random.uniform(0.0, 10.0), 2)
    except Exception as e:
        print(f"[metrics] error computing brightness, falling back to synthetic: {e}")
        brightness = round(random.uniform(0.0, 10.0), 2)

    return {
        "ts": now_ts,
        "brightness": brightness,
    }


# -----------------------------
# Config & Healthcheck
# -----------------------------

@app.get("/config")
def get_config():
    """
    Static/dynamic configuration for this node.
    This is what Neuron + consumers can use for discovery.
    """
    return {
        "device_id": DEVICE_ID,
        "location": {
            "lat": LOCATION_LAT,
            "lon": LOCATION_LON,
            "label": LOCATION_LABEL,
        },
        "sensors": {
            "brightness": True,
            "camera": CAMERA_AVAILABLE,
            "temp_humidity": True,  # simulated for now
        },
        "version": "0.1.0",
    }


@app.get("/health")
def health():
    """
    Basic healthcheck.
    """
    now = time.time()
    uptime_sec = int(now - START_TS)

    last_snap_ts = LAST_SNAPSHOT_TS
    last_snap_age = None
    if last_snap_ts is not None:
        last_snap_age = int(now - last_snap_ts)

    status = "ok" if LAST_SNAPSHOT_OK else "degraded"

    return {
        "status": status,
        "uptime_sec": uptime_sec,
        "last_snapshot_ok": LAST_SNAPSHOT_OK,
        "last_snapshot_ts": LAST_SNAPSHOT_TS,
        "last_snapshot_age_sec": last_snap_age,
        "device_id": DEVICE_ID,
        "camera_available": CAMERA_AVAILABLE,
    }
