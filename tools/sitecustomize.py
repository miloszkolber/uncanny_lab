"""Conversion subprocess defense in depth: bundled legacy code may not open sockets."""
import socket

def _blocked(self, address):
    raise OSError("network access is disabled during model conversion")

socket.socket.connect = _blocked
