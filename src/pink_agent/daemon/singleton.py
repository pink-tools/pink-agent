"""
Single instance enforcement - ensures only one instance of the process runs.
"""

import sys
import os
import psutil

from pink_agent.config import VERBOSE_MODE, SINGLETON_IDENTIFIERS


def _find_root_process(proc: psutil.Process, excluded_pids: list[int]) -> psutil.Process:
    """
    Find root process (topmost non-system parent).

    Walks up the process tree until reaching system process or excluded PID.
    """
    root = proc
    try:
        while root.parent():
            parent = root.parent()

            # Stop if parent is excluded (our own parent chain)
            if parent.pid in excluded_pids:
                break

            # Stop if parent is system process (PID <= 1000)
            if parent.pid <= 1000:
                break

            root = parent
    except (psutil.NoSuchProcess, psutil.AccessDenied):
        pass

    return root


def _kill_process_tree(root: psutil.Process, verbose: bool) -> int:
    """Kill process and all its children recursively."""
    killed = 0

    try:
        # Get all children first
        children = root.children(recursive=True)

        if verbose:
            print(f"[Singleton]   -> Killing {len(children)} children of PID {root.pid}...")

        # Kill children bottom-up
        for child in reversed(children):
            try:
                if verbose:
                    print(f"[Singleton]      -> Child PID {child.pid}")
                child.kill()
                killed += 1
            except (psutil.NoSuchProcess, psutil.AccessDenied):
                pass

        # Kill root
        if verbose:
            print(f"[Singleton]   -> Killing root PID {root.pid}")
        root.kill()
        killed += 1

    except (psutil.NoSuchProcess, psutil.AccessDenied):
        try:
            root.kill()
            killed += 1
        except:
            pass

    return killed


def ensure_single_instance(process_name: str) -> None:
    """
    Kill all other instances of this process and ensure only one runs.

    Strategy:
    - Find all Python processes with target modules in cmdline
    - For each found process: climb to root of process tree
    - Kill entire tree from root (handles wrappers like caffeinate/uv)
    - Works regardless of how process was launched

    Args:
        process_name: Not used anymore, kept for compatibility
    """
    current_pid = os.getpid()
    killed_count = 0

    # Get parent chain to avoid killing ourselves
    parent_chain = []
    try:
        current_proc = psutil.Process(current_pid)
        while current_proc.parent():
            parent_chain.append(current_proc.parent().pid)
            current_proc = current_proc.parent()
    except (psutil.NoSuchProcess, psutil.AccessDenied):
        pass

    excluded_pids = [current_pid] + parent_chain

    if VERBOSE_MODE:
        print(f"[Singleton] Current PID: {current_pid}, Parent chain: {parent_chain}")
        print(f"[Singleton] Looking for processes with identifiers: {SINGLETON_IDENTIFIERS}")

    # Track already killed root PIDs to avoid duplicates
    killed_roots = set()

    # Find all processes with our project identifiers
    for proc in psutil.process_iter(['pid', 'name', 'cmdline']):
        try:
            if not proc.info['cmdline']:
                continue

            # Skip excluded PIDs
            if proc.info['pid'] in excluded_pids:
                continue

            cmdline = ' '.join(proc.info['cmdline'])

            # Check if any project identifier in cmdline
            for identifier in SINGLETON_IDENTIFIERS:
                if identifier in cmdline:
                    if VERBOSE_MODE:
                        print(f"[Singleton] Found target process PID {proc.info['pid']}: {cmdline}")

                    # Find root of this process tree (use existing proc object to avoid race condition)
                    root = _find_root_process(proc, excluded_pids)

                    # Skip if already killed this root
                    if root.pid in killed_roots:
                        if VERBOSE_MODE:
                            print(f"[Singleton]   -> Root PID {root.pid} already killed, skipping")
                        break

                    if VERBOSE_MODE:
                        try:
                            root_cmdline = ' '.join(root.cmdline()) if root.cmdline() else 'N/A'
                            print(f"[Singleton]   -> Root process PID {root.pid}: {root_cmdline}")
                        except:
                            print(f"[Singleton]   -> Root process PID {root.pid}")

                    # Kill entire tree from root
                    killed = _kill_process_tree(root, VERBOSE_MODE)
                    killed_count += killed
                    killed_roots.add(root.pid)

                    break

        except (psutil.NoSuchProcess, psutil.AccessDenied, psutil.ZombieProcess):
            pass
        except Exception as e:
            if VERBOSE_MODE:
                print(f"[Singleton] Error: {e}")

    if killed_count > 0 and VERBOSE_MODE:
        print(f"[Singleton] Total killed: {killed_count} process(es)")
    elif VERBOSE_MODE:
        print(f"[Singleton] No existing instances found")
