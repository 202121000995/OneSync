#!/usr/bin/env python3
import struct
import sys
from pathlib import Path


def align4(data):
    while len(data) % 4:
        data += b"\x00"
    return data


def res_name(value):
    return struct.pack("<HH", 0xFFFF, value)


def resource_record(resource_type, resource_id, data, language=0x0804):
    header_fields = (
        res_name(resource_type)
        + res_name(resource_id)
        + struct.pack("<IHHII", 0, 0x1030, language, 0, 0)
    )
    header_size = 8 + len(header_fields)
    return align4(
        struct.pack("<II", len(data), header_size)
        + header_fields
        + align4(bytearray(data))
    )


def read_ico(path):
    blob = Path(path).read_bytes()
    reserved, icon_type, count = struct.unpack_from("<HHH", blob, 0)
    if reserved != 0 or icon_type != 1 or count < 1:
        raise SystemExit("not a Windows .ico file")
    entries = []
    for index in range(count):
        offset = 6 + index * 16
        width, height, colors, _ = struct.unpack_from("<BBBB", blob, offset)
        planes, bit_count, size, image_offset = struct.unpack_from("<HHII", blob, offset + 4)
        entries.append({
            "width": width,
            "height": height,
            "colors": colors,
            "planes": planes,
            "bit_count": bit_count,
            "data": blob[image_offset:image_offset + size],
        })
    return entries


def build_group_icon(entries, first_icon_id=1):
    data = bytearray(struct.pack("<HHH", 0, 1, len(entries)))
    for index, entry in enumerate(entries):
        data += struct.pack(
            "<BBBBHHIH",
            entry["width"],
            entry["height"],
            entry["colors"],
            0,
            entry["planes"],
            entry["bit_count"],
            len(entry["data"]),
            first_icon_id + index,
        )
    return bytes(data)


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: make_icon_res.py app.ico app_icon.res")
    entries = read_ico(sys.argv[1])
    out = bytearray()
    out += struct.pack("<II", 0, 32)
    out += res_name(0)
    out += res_name(0)
    out += struct.pack("<IHHII", 0, 0, 0, 0, 0)
    for index, entry in enumerate(entries):
        out += resource_record(3, index + 1, entry["data"])
    out += resource_record(14, 1, build_group_icon(entries))
    Path(sys.argv[2]).write_bytes(out)


if __name__ == "__main__":
    main()
