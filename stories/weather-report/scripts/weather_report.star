# weather_report.star — deterministic glue behind host.starlark.run.
#
# Turns a free-text place name + a mode ("forecast" | "climate") into a tidy,
# display-ready weather report. It does it the way host.starlark.run is meant to
# be used: a couple of plain JSON HTTP calls to a free, key-less API
# (Open-Meteo) plus the kind of fiddly-but-deterministic reshaping that is
# awkward in YAML effects and too small for a bespoke Go host handler —
#
#   * geocode the free-text place to a lat/lon (always),
#   * then EITHER a 5-day forecast OR a full-year (2023) climate profile that
#     aggregates 365 daily means into 12 calendar months.
#
# Everything here is deterministic and replayable: no clock, no randomness, no
# filesystem, no environment — only ctx.inputs / ctx.http plus the json + math
# stdlib. A recorded run replays byte-for-byte from a cassette.
#
# ── Interface (authoritative copy lives in weather_report.star.yaml) ──────────
# These INPUTS/OUTPUTS dicts are a CONVENTION for the human reader; the engine
# ignores them and validates against the sidecar.
INPUTS = {
    "location": "string — free-text place name, e.g. 'Tokyo' or 'San Francisco'",
    "mode": "string — 'forecast' (5-day) or 'climate' (2023 monthly profile)",
}
OUTPUTS = {
    "place": "string — resolved 'City, Region, Country' label",
    "coords": "string — 'lat°N, lon°E · tz <zone>'",
    "headline": "string — one-line summary for the chosen mode",
    "current": "object — forecast: {temp, humidity, wind, desc, *_unit}; climate: {}",
    "rows": "list — forecast: per-day rows; climate: per-month rows",
    "climate_summary": "object — climate: {year, annual_mean, warmest, …}; forecast: {}",
}

# Open-Meteo endpoints — all free and require no API key.
GEOCODE = "https://geocoding-api.open-meteo.com/v1/search"
FORECAST = "https://api.open-meteo.com/v1/forecast"
ARCHIVE = "https://archive-api.open-meteo.com/v1/archive"

# A representative recent full year for the climate profile. Fixed (not "today")
# so the climate request is fully deterministic and its cassette never drifts.
CLIMATE_YEAR = "2023"

# WMO weather-interpretation codes → short human label. Open-Meteo encodes the
# sky/precip condition as a single integer; this is the standard mapping.
WEATHER_CODES = {
    0: "clear sky",
    1: "mainly clear",
    2: "partly cloudy",
    3: "overcast",
    45: "fog",
    48: "rime fog",
    51: "light drizzle",
    53: "drizzle",
    55: "dense drizzle",
    56: "freezing drizzle",
    57: "dense freezing drizzle",
    61: "light rain",
    63: "rain",
    65: "heavy rain",
    66: "freezing rain",
    67: "heavy freezing rain",
    71: "light snow",
    73: "snow",
    75: "heavy snow",
    77: "snow grains",
    80: "light rain showers",
    81: "rain showers",
    82: "violent rain showers",
    85: "snow showers",
    86: "heavy snow showers",
    95: "thunderstorm",
    96: "thunderstorm with hail",
    99: "thunderstorm with heavy hail",
}

MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
          "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]


def describe(code):
    """WMO code → label, tolerant of a missing/None/unknown code."""
    if code == None:
        return "unknown"
    return WEATHER_CODES.get(int(code), "code %d" % int(code))


def fixed(x, places):
    """Format a float to `places` decimals, deterministically.

    Starlark's `%` operator has no precision specifier (`%.2f` is rejected), so
    we round and assemble the string ourselves with integer arithmetic — keeping
    the whole script free of any float-formatting locale/precision surprises.
    """
    if x == None:
        return "—"
    neg = x < 0
    scale = 1
    for _ in range(places):
        scale *= 10
    n = int(math.floor(abs(x) * scale + 0.5))   # round half up on the magnitude
    if places == 0:
        s = "%d" % n
    else:
        frac = "%d" % (n % scale)
        frac = ("0" * (places - len(frac))) + frac   # zero-pad to `places`
        s = "%d.%s" % (n // scale, frac)
    return ("-" + s) if neg else s


def fmt1(x):
    """One-decimal string; None renders as an em dash so tables stay aligned."""
    return fixed(x, 1)


def geocode(ctx, place):
    """Resolve a free-text place to its first geocoding match (or fail())."""
    # Encode spaces so multi-word names ('San Francisco') form a valid URL; the
    # geocoding API accepts '+' for a space in the query.
    q = place.strip().replace(" ", "+")
    resp = ctx.http.get("%s?name=%s&count=1&language=en&format=json" % (GEOCODE, q))
    if not resp:
        fail("geocoding failed: HTTP %d for %r" % (resp.status, place))
    results = resp.json().get("results", None)
    if not results:
        fail("no place found matching %r — try a city name like \"Tokyo\" or \"Paris\"" % place)
    return results[0]


def place_label(geo, fallback):
    """'City, Region, Country', dropping a region that just repeats the city."""
    name = geo.get("name", fallback)
    admin1 = geo.get("admin1", "")
    country = geo.get("country", "")
    parts = [name]
    if admin1 and admin1 != name:
        parts.append(admin1)
    if country:
        parts.append(country)
    return ", ".join(parts)


def coords_label(lat, lon, tz):
    ns = "N" if lat >= 0 else "S"
    ew = "E" if lon >= 0 else "W"
    return "%s°%s, %s°%s · tz %s" % (fixed(abs(lat), 2), ns, fixed(abs(lon), 2), ew, tz)


def main(ctx):
    place = ctx.inputs["location"]
    mode = ctx.inputs["mode"]

    geo = geocode(ctx, place)
    lat = geo["latitude"]
    lon = geo["longitude"]
    label = place_label(geo, place)
    coords = coords_label(lat, lon, geo.get("timezone", ""))

    if mode == "forecast":
        return forecast_report(ctx, lat, lon, label, coords)
    elif mode == "climate":
        return climate_report(ctx, lat, lon, label, coords)
    else:
        fail("unknown mode %r (want \"forecast\" or \"climate\")" % mode)


def forecast_report(ctx, lat, lon, label, coords):
    url = ("%s?latitude=%s&longitude=%s&timezone=auto&forecast_days=5" +
           "&current=temperature_2m,relative_humidity_2m,weather_code,wind_speed_10m" +
           "&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,wind_speed_10m_max") % (FORECAST, fixed(lat, 4), fixed(lon, 4))
    resp = ctx.http.get(url)
    if not resp:
        fail("forecast lookup failed: HTTP %d" % resp.status)
    data = resp.json()

    cur = data.get("current", {})
    units = data.get("current_units", {})
    current = {
        "temp": fmt1(cur.get("temperature_2m")),
        "humidity": "%d" % int(cur.get("relative_humidity_2m", 0)),
        "wind": fmt1(cur.get("wind_speed_10m")),
        "desc": describe(cur.get("weather_code")),
        "temp_unit": units.get("temperature_2m", "°C"),
        "wind_unit": units.get("wind_speed_10m", "km/h"),
    }

    daily = data.get("daily", {})
    times = daily.get("time", [])
    codes = daily.get("weather_code", [])
    hi = daily.get("temperature_2m_max", [])
    lo = daily.get("temperature_2m_min", [])
    pr = daily.get("precipitation_sum", [])
    wind = daily.get("wind_speed_10m_max", [])

    rows = []
    for i in range(len(times)):
        rows.append({
            "date": times[i],
            "desc": describe(codes[i]),
            "hi": fmt1(hi[i]),
            "lo": fmt1(lo[i]),
            "precip": fmt1(pr[i]),
            "wind": fmt1(wind[i]),
        })

    headline = "Now %s%s, %s · humidity %s%% · wind %s %s" % (
        current["temp"], current["temp_unit"], current["desc"],
        current["humidity"], current["wind"], current["wind_unit"])

    return {
        "place": label,
        "coords": coords,
        "headline": headline,
        "current": current,
        "rows": rows,
        "climate_summary": {},
    }


def climate_report(ctx, lat, lon, label, coords):
    url = ("%s?latitude=%s&longitude=%s&timezone=auto" +
           "&start_date=%s-01-01&end_date=%s-12-31" +
           "&daily=temperature_2m_mean,precipitation_sum") % (ARCHIVE, fixed(lat, 4), fixed(lon, 4), CLIMATE_YEAR, CLIMATE_YEAR)
    resp = ctx.http.get(url)
    if not resp:
        fail("climate lookup failed: HTTP %d" % resp.status)
    daily = resp.json().get("daily", {})
    times = daily.get("time", [])
    temps = daily.get("temperature_2m_mean", [])
    precip = daily.get("precipitation_sum", [])

    # Bucket the 365 daily values into 12 calendar months by parsing the
    # "YYYY-MM-DD" date prefix — deterministic wrangling that would be ugly in
    # YAML. Skip null days (the archive leaves gaps for some grids).
    sum_t = [0.0] * 12
    cnt_t = [0] * 12
    sum_p = [0.0] * 12
    for i in range(len(times)):
        m = int(times[i][5:7]) - 1
        if temps[i] != None:
            sum_t[m] += temps[i]
            cnt_t[m] += 1
        if precip[i] != None:
            sum_p[m] += precip[i]

    rows = []
    total_t = 0.0
    total_n = 0
    total_p = 0.0
    warm_m, warm_v = 0, None
    cold_m, cold_v = 0, None
    wet_m, wet_v = 0, -1.0
    for m in range(12):
        mean_t = (sum_t[m] / cnt_t[m]) if cnt_t[m] > 0 else None
        rows.append({
            "month": MONTHS[m],
            "mean_temp": fmt1(mean_t),
            "precip": fmt1(sum_p[m]),
        })
        if mean_t != None:
            total_t += sum_t[m]
            total_n += cnt_t[m]
            if warm_v == None or mean_t > warm_v:
                warm_m, warm_v = m, mean_t
            if cold_v == None or mean_t < cold_v:
                cold_m, cold_v = m, mean_t
        total_p += sum_p[m]
        if sum_p[m] > wet_v:
            wet_m, wet_v = m, sum_p[m]

    annual_mean = (total_t / total_n) if total_n > 0 else None
    summary = {
        "year": CLIMATE_YEAR,
        "annual_mean": fmt1(annual_mean),
        "warmest": "%s (%s°C)" % (MONTHS[warm_m], fmt1(warm_v)),
        "coldest": "%s (%s°C)" % (MONTHS[cold_m], fmt1(cold_v)),
        "wettest": "%s (%s mm)" % (MONTHS[wet_m], fmt1(wet_v)),
        "precip_total": "%s mm" % fmt1(total_p),
    }
    headline = "%s annual mean %s°C · warmest %s · wettest %s · %s mm total" % (
        CLIMATE_YEAR, fmt1(annual_mean), MONTHS[warm_m], MONTHS[wet_m], fmt1(total_p))

    return {
        "place": label,
        "coords": coords,
        "headline": headline,
        "current": {},
        "rows": rows,
        "climate_summary": summary,
    }
