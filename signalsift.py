import streamlit as st
import sqlite3
import datetime
from collections import defaultdict
import pandas as pd
import tempfile
import os

def connect_to_db(db_file):
    with tempfile.NamedTemporaryFile(delete=False) as tmp_file:
        tmp_file.write(db_file.getvalue())
        tmp_file_path = tmp_file.name
    conn = sqlite3.connect(tmp_file_path)
    return conn, tmp_file_path

def get_summary(conn):
    cursor = conn.cursor()
    
    cursor.execute("SELECT MIN(ts_sec), MAX(ts_sec) FROM packets")
    start_time, end_time = cursor.fetchone()
    
    cursor.execute("SELECT COUNT(*) FROM devices")
    device_count = cursor.fetchone()[0]
    
    cursor.execute("SELECT COUNT(*) FROM packets")
    packet_count = cursor.fetchone()[0]
    
    cursor.execute("SELECT COUNT(DISTINCT devmac) FROM devices")
    unique_macs = cursor.fetchone()[0]
    
    return {
        "Time Range": f"{datetime.datetime.fromtimestamp(start_time)} - {datetime.datetime.fromtimestamp(end_time)}",
        "Duration": f"{(end_time - start_time) / 3600:.2f} hours",
        "Device Count": device_count,
        "Packet Count": packet_count,
        "Unique MACs": unique_macs
    }

def get_device_packet_counts(conn):
    cursor = conn.cursor()
    device_packet_counts = defaultdict(int)
    
    cursor.execute("SELECT sourcemac, COUNT(*) FROM packets GROUP BY sourcemac")
    for row in cursor.fetchall():
        device_packet_counts[row[0]] += row[1]
    
    return device_packet_counts

def get_devices(conn, packet_counts):
    cursor = conn.cursor()
    cursor.execute("SELECT devmac, phyname, type, strongest_signal FROM devices")
    devices_info = {}
    for row in cursor.fetchall():
        devmac = row[0]
        devices_info[devmac] = {
            "phyname": row[1],
            "type": row[2],
            "signal": row[3],
            "packet_count": packet_counts.get(devmac, 0)
        }
    return devices_info

def compare_captures(baseline, followup):
    new_devices = set(followup.keys()) - set(baseline.keys())
    missing_devices = set(baseline.keys()) - set(followup.keys())
    return new_devices, missing_devices

def main():
    st.title("Kismet Capture Comparison")

    baseline_file = st.file_uploader("Choose baseline Kismet database", type="kismet")
    followup_file = st.file_uploader("Choose follow-up Kismet database", type="kismet")

    if baseline_file and followup_file:
        baseline_conn, baseline_path = connect_to_db(baseline_file)
        followup_conn, followup_path = connect_to_db(followup_file)

        st.header("Baseline Summary")
        baseline_summary = get_summary(baseline_conn)
        st.table(pd.DataFrame.from_dict(baseline_summary, orient='index', columns=['Value']))

        st.header("Follow-up Summary")
        followup_summary = get_summary(followup_conn)
        st.table(pd.DataFrame.from_dict(followup_summary, orient='index', columns=['Value']))

        baseline_packet_counts = get_device_packet_counts(baseline_conn)
        followup_packet_counts = get_device_packet_counts(followup_conn)

        baseline_devices = get_devices(baseline_conn, baseline_packet_counts)
        followup_devices = get_devices(followup_conn, followup_packet_counts)

        st.header("Top 10 Most Active Devices in Baseline")
        baseline_top_devices = sorted(baseline_devices.items(), key=lambda x: x[1]['packet_count'], reverse=True)[:10]
        st.table(pd.DataFrame([(mac, info['phyname'], info['type'], info['signal'], info['packet_count']) 
                               for mac, info in baseline_top_devices], 
                              columns=["MAC", "PHY", "Type", "Signal", "Packet Count"]))

        st.header("Top 10 Most Active Devices in Follow-up")
        followup_top_devices = sorted(followup_devices.items(), key=lambda x: x[1]['packet_count'], reverse=True)[:10]
        st.table(pd.DataFrame([(mac, info['phyname'], info['type'], info['signal'], info['packet_count']) 
                               for mac, info in followup_top_devices], 
                              columns=["MAC", "PHY", "Type", "Signal", "Packet Count"]))

        new_devices, missing_devices = compare_captures(baseline_devices, followup_devices)

        st.header(f"New Devices Detected: {len(new_devices)}")
        if new_devices:
            st.table(pd.DataFrame([(mac, followup_devices[mac]["phyname"], followup_devices[mac]["type"], 
                                    followup_devices[mac]["signal"], followup_devices[mac]["packet_count"]) 
                                   for mac in new_devices], 
                                  columns=["MAC", "PHY", "Type", "Signal", "Packet Count"]))

        st.header(f"Missing Devices (Present in Baseline, Not in Follow-up): {len(missing_devices)}")
        if missing_devices:
            st.table(pd.DataFrame([(mac, baseline_devices[mac]["phyname"], baseline_devices[mac]["type"], 
                                    baseline_devices[mac]["signal"], baseline_devices[mac]["packet_count"]) 
                                   for mac in missing_devices], 
                                  columns=["MAC", "PHY", "Type", "Signal", "Packet Count"]))

        baseline_conn.close()
        followup_conn.close()
        os.unlink(baseline_path)
        os.unlink(followup_path)

if __name__ == "__main__":
    main()
